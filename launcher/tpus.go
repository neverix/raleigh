package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os/exec"
	"strings"
)

type TPUWatcher struct {
	project      string
	zone         string
	instanceType string
	id           string

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
	case "RUNNING":
		return tpuStatusRunning
	case "STOPPING":
		return tpuStatusStopping
	case "STOPPED":
		return tpuStatusStopped
	}
	return tpuStatusError
}

type gCloudTPU struct {
	Status string `json:"status"`
}

type tpuInfo struct {
	Status          string `json:"status"`
	IP              string `json:"networkEndPoints[0].accessConfig.externalIp"`
	InternalIP      string `json:"networkEndPoints[0].ipAddress"`
	InternalPort    int    `json:"networkEndPoints[0].port"`
	Zone            string `json:"zone"`
	Project         string `json:"project"`
	AcceleratorType string `json:"acceleratorType"`
	Version         string `json:"version"`
	Preemptible     bool   `json:"preemptible"`
	Health          string `json:"health"`
}

type tpuInfoRaw struct {
	Status           string `json:"status"`
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

func (t *TPUWatcher) checkStatus() (tpuInfo, tpuStatus) {
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
		Status:          tpuInformation.Status,
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
	t.latestStatus = tpuStatusFromString(tpu.Status)
	return t.latestInfo, t.latestStatus
}

func (t *TPUWatcher) scp(localPath string, remotePath string, user string) error {
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

func (t *TPUWatcher) ssh(user string, command string) *exec.Cmd {
	return exec.Command("gcloud", "compute", "tpus", "tpu-vm", "ssh", user+"@"+t.id, "--project", t.project, "--zone", t.zone, "--command", command)
}

func (t *TPUWatcher) start() error {
	cmd := exec.Command("gcloud", "compute", "tpus", "tpu-vm", "create", t.id, "--project", t.project, "--zone", t.zone, "--accelerator-type", t.instanceType, "--version", "v2-alpha", "--preemptible")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("error starting tpu: %v\n", stderr.String())
		return err
	}
	return nil
}

func (t *TPUWatcher) delete() error {
	return exec.Command("gcloud", "compute", "tpus", "tpu-vm", "delete", t.id, "--project", t.project, "--zone", t.zone, "--quiet").Run()
}
