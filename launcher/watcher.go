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
			fmt.Printf("Error creating installer: %v\n", err)
			continue
		}

		updateStatus := func() {
			status.mutex.Lock()
			status.status = TpuStatusUpdate{
				id:        id,
				status:    installer.tpuController.latestStatus,
				info:      installer.tpuController.latestInfo,
				installed: installer.basicsInstalled,
			}
			status.mutex.Unlock()
			updateChan <- status.status
		}
		updateStatus()

		fmt.Printf("newInstaller.tpuController.latestStatus: %v\n", newInstaller.tpuController.latestStatus)

		if newInstaller.tpuController.latestStatus != tpuStatusRunning {
			switch newInstaller.tpuController.latestStatus {
			case tpuStatusNonexistent:
				newInstaller.tpuController.start()
			case tpuStatusError:
				newInstaller.tpuController.delete()
			}
			continue
		}
		*installer = *newInstaller

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
		go Watch(cfg, i, tpuInstallers[i], channel, &statuses[i])
	}
	return &TpuWatcher{
		tpuInstallers: tpuInstallers,
		updates:       channel,
		statuses:      statuses,
	}
}
