package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/net/context"
)

type TpuController struct {
	project      string
	zone         string
	instanceType string
	id           string
	preemptible  bool
	spot         bool
	latestInfo   tpuInfo
	latestStatus tpuStatus
}

type tpuStatus int

const (
	tpuStatusNonexistent tpuStatus = iota
	tpuStatusCreating
	tpuStatusRunning
	tpuStatusStopping
	tpuStatusStopped
	tpuStatusDeleting
	tpuStatusError
)

func tpuStatusFromString(s string) tpuStatus {
	switch s {
	case "CREATING":
		return tpuStatusCreating
	case "READY":
		return tpuStatusRunning
	case "STOPPING":
		return tpuStatusStopping
	case "STOPPED":
		return tpuStatusStopped
	}
	return tpuStatusError
}

type gCloudTPU struct {
	Status string `json:"state"`
}

type tpuInfo struct {
	Status          tpuStatus
	IP              string
	InternalIP      string
	InternalPort    int
	Zone            string
	Project         string
	AcceleratorType string
	Version         string
	Preemptible     bool
	Spot            bool
	Health          string
}

type tpuInfoRaw struct {
	Status           string `json:"state"`
	NetworkEndPoints []struct {
		AccessConfig struct {
			ExternalIP string `json:"externalIp"`
		} `json:"accessConfig"`
		IPAddress string `json:"ipAddress"`
		Port      int    `json:"port"`
	} `json:"networkEndPoints"`
	Zone             string `json:"zone"`
	Project          string `json:"project"`
	AcceleratorType  string `json:"acceleratorType"`
	Version          string `json:"version"`
	SchedulingConfig struct {
		Preemptible bool `json:"preemptible"`
	} `json:"schedulingConfig"`
	Health string `json:"health"`
}

func (t *TpuController) checkStatus() (tpuInfo, tpuStatus) {
	cmd := exec.Command("gcloud", "compute", "tpus", "tpu-vm", "describe", t.id, "--project", t.project, "--zone", t.zone, "--format", "json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	tpuJson, err := cmd.Output()
	if err != nil {
		if strings.HasPrefix(stderr.String(), "ERROR: (gcloud.compute.tpus.tpu-vm.describe) NOT_FOUND: ") {
			t.latestStatus = tpuStatusNonexistent
			return t.latestInfo, t.latestStatus
		}
		log.Printf("fatal error getting tpu: %v\n", stderr.String())
		t.latestStatus = tpuStatusError
		return t.latestInfo, t.latestStatus
	}
	var tpu gCloudTPU
	err = json.Unmarshal(tpuJson, &tpu)
	if err != nil {
		log.Printf("fatal error unmarshalling tpu: %v\n", err)
		t.latestStatus = tpuStatusError
		return t.latestInfo, t.latestStatus
	}
	var tpuInformation tpuInfoRaw
	err = json.Unmarshal(tpuJson, &tpuInformation)
	if err != nil {
		log.Printf("fatal error unmarshalling tpu: %v\n", err)
		return tpuInfo{}, tpuStatusError
	}
	t.latestInfo = tpuInfo{
		Status:          tpuStatusFromString(tpuInformation.Status),
		IP:              tpuInformation.NetworkEndPoints[0].AccessConfig.ExternalIP,
		InternalIP:      tpuInformation.NetworkEndPoints[0].IPAddress,
		InternalPort:    tpuInformation.NetworkEndPoints[0].Port,
		Zone:            tpuInformation.Zone,
		Project:         tpuInformation.Project,
		AcceleratorType: tpuInformation.AcceleratorType,
		Version:         tpuInformation.Version,
		Preemptible:     tpuInformation.SchedulingConfig.Preemptible,
		Health:          tpuInformation.Health,
	}
	t.latestStatus = t.latestInfo.Status
	return t.latestInfo, t.latestStatus
}

func (t *TpuController) scp(localPath string, remotePath string, user string) error {
	cmd := exec.Command("gcloud", "compute", "tpus", "tpu-vm", "scp", "--recurse", localPath, user+"@"+t.id+":"+remotePath, "--project", t.project, "--zone", t.zone)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error scp: %v", stderr.String())
	}
	return nil
}

func (t *TpuController) rsync(localPath string, remotePath string, user string) error {
	if t.latestInfo.Status != tpuStatusRunning {
		return fmt.Errorf("tpu must be running to rsync")
	}
	cmd := exec.Command("rsync", "-avz", localPath+"/", user+"@"+t.latestInfo.IP+":"+remotePath, "-e", "ssh -i ~/.ssh/google_compute_engine -o \"StrictHostKeyChecking=no\"")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error rsync: %v", stderr.String())
	}
	return nil
}

func (t *TpuController) checkProcessRunning(pid int) (bool, error) {
	if pid == -1 {
		return false, nil
	}
	cmd := t.ssh("root", fmt.Sprintf("kill -0 %d", pid))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if strings.Contains(stderr.String(), "No such process") {
			return false, nil
		}
		return false, fmt.Errorf("error checking process running: %v", stderr.String())
	}
	return true, nil
}

func (t *TpuController) killProcess(pid int, retry time.Duration, ctx context.Context) error {
	if pid == -1 {
		return nil
	}
	cmd := t.ssh("root", fmt.Sprintf("kill %d", pid))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if strings.Contains(stderr.String(), "No such process") {
			return nil
		}
		return fmt.Errorf("error killing process: %v", stderr.String())
	}

	ticker := time.NewTicker(retry)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			checkCmd := t.ssh("root", fmt.Sprintf("kill %d", pid))
			var checkErr bytes.Buffer
			checkCmd.Stderr = &checkErr
			err := checkCmd.Run()
			if err != nil {
				if strings.Contains(checkErr.String(), "No such process") {
					return nil
				}
				return fmt.Errorf("error killing process: %v", checkErr.String())
			}
		}
	}
}

func (t *TpuController) scpFrom(user string, localPath string, remotePath string) error {
	cmd := exec.Command("gcloud", "compute", "tpus", "tpu-vm", "scp", user+"@"+t.id+":"+remotePath, localPath, "--project", t.project, "--zone", t.zone)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error scp from: %v", stderr.String())
	}
	return nil
}

func (t *TpuController) ssh(user string, command string) *exec.Cmd {
	return exec.Command("gcloud", "compute", "tpus", "tpu-vm", "ssh", user+"@"+t.id, "--project", t.project, "--zone", t.zone, "--command", command)
}

func (t *TpuController) start() error {
	args := []string{"compute", "tpus", "tpu-vm", "create", t.id, "--project", t.project, "--zone", t.zone, "--accelerator-type", t.instanceType, "--version", "tpu-ubuntu2204-base"}
	if t.preemptible {
		args = append(args, "--preemptible")
	}
	if t.spot {
		args = append(args, "--spot")
	}
	cmd := exec.Command("gcloud", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error starting tpu: %v", stderr.String())
	}
	return nil
}

func (t *TpuController) delete() error {
	return exec.Command("gcloud", "compute", "tpus", "tpu-vm", "delete", t.id, "--project", t.project, "--zone", t.zone, "--quiet").Run()
}
