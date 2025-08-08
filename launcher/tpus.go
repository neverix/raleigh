package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

type TpuController struct {
	project      string
	zone         string
	instanceType string
	id           string
	preemptible  bool

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
		log.Printf("error scp: %v\n", stderr.String())
		return err
	}
	return nil
}

func (t *TpuController) rsync(localPath string, remotePath string, user string) error {
	if t.latestInfo.Status != tpuStatusRunning {
		return fmt.Errorf("tpu must be running to rsync")
	}
	cmd := exec.Command("rsync", "-avz", localPath, user+"@"+t.latestInfo.IP+":"+remotePath, "-e", "ssh -i ~/.ssh/google_compute_engine -o \"StrictHostKeyChecking=no\nUserKnownHostsFile=/dev/null\"")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("error rsync: %v\n", stderr.String())
		return err
	}
	return nil
}

func (t *TpuController) scpFrom(user string, localPath string, remotePath string) error {
	cmd := exec.Command("gcloud", "compute", "tpus", "tpu-vm", "scp", user+"@"+t.id+":"+remotePath, localPath, "--project", t.project, "--zone", t.zone)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("error scp from: %v\n", stderr.String())
		return err
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
	cmd := exec.Command("gcloud", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("error starting tpu: %v\n", stderr.String())
		return err
	}
	return nil
}

func (t *TpuController) delete() error {
	return exec.Command("gcloud", "compute", "tpus", "tpu-vm", "delete", t.id, "--project", t.project, "--zone", t.zone, "--quiet").Run()
}
