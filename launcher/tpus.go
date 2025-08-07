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

func (t *TPUWatcher) checkStatus() tpuStatus {
	cmd := exec.Command("gcloud", "compute", "tpus", "describe", t.id, "--project", t.project, "--zone", t.zone, "--format", "json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	tpuJson, err := cmd.Output()
	if err != nil {
		if strings.HasPrefix(stderr.String(), "ERROR: (gcloud.compute.tpus.describe) NOT_FOUND: ") {
			return tpuStatusNonexistent
		}
		log.Printf("fatal error getting tpu: %v\n", err)
		return tpuStatusError
	}
	var tpu gCloudTPU
	err = json.Unmarshal(tpuJson, &tpu)
	if err != nil {
		log.Printf("fatal error unmarshalling tpu: %v\n", err)
		return tpuStatusError
	}
	return tpuStatusFromString(tpu.Status)
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



