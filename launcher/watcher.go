package main

import (
	"fmt"
	"sync"
	"time"
)

type TpuStatusUpdate struct {
	id        int
	status    tpuStatus
	info      tpuInfo
	installed bool
	cloned    bool
}

type TpuCurrentStatus struct {
	mutex  sync.Mutex
	status TpuStatusUpdate
}

type TpuWatcher struct {
	tpuInstallers []*TpuInstaller
	updates       chan TpuStatusUpdate
	statuses      []TpuCurrentStatus
}

func Watch(cfg TpuConfig, id int, installer *TpuInstaller, updateChan chan TpuStatusUpdate, status *TpuCurrentStatus) {
	firstIteration := true
	for {
		if !firstIteration {
			time.Sleep(5 * time.Second)
		}
		firstIteration = false
		newInstaller, err := NewTpuInstaller(cfg, fmt.Sprintf("raleigh-tpu-%d", id))
		if err != nil {
			continue
		}
		*installer = *newInstaller

		updateStatus := func() {
			status.mutex.Lock()
			status.status = TpuStatusUpdate{
				id:        id,
				status:    installer.tpuController.latestStatus,
				info:      installer.tpuController.latestInfo,
				installed: installer.basicsInstalled,
				cloned:    installer.repoCloned,
			}
			status.mutex.Unlock()
			updateChan <- status.status
		}
		updateStatus()

		if installer.tpuController.latestStatus != tpuStatusRunning {
			switch installer.tpuController.latestStatus {
			case tpuStatusNonexistent:
				installer.tpuController.start()
			case tpuStatusError:
				installer.tpuController.delete()
			}
			continue
		}

		if !installer.basicsInstalled {
			installer.InstallBasics()
			updateStatus()
		}

		if !installer.repoCloned {
			installer.CloneRepo()
			updateStatus()
		}
	}
}

func NewTpuWatcher(cfg TpuConfig, n int) *TpuWatcher {
	tpuInstallers := make([]*TpuInstaller, n)
	channel := make(chan TpuStatusUpdate)
	statuses := make([]TpuCurrentStatus, n)
	for i := 0; i < n; i++ {
		tpuInstallers[i] = &TpuInstaller{}
		go Watch(cfg, i, tpuInstallers[i], channel, &statuses[i])
	}
	return &TpuWatcher{
		tpuInstallers: tpuInstallers,
		updates:       channel,
		statuses:      statuses,
	}
}
