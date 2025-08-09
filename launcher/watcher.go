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
	running   bool
	err       error
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
		updateStatus := func(err error) {
			status.mutex.Lock()
			if err != nil {
				status.status.id = id
				status.status.err = err
			} else {
				status.status = TpuStatusUpdate{
					id:        id,
					status:    installer.tpuController.latestStatus,
					info:      installer.tpuController.latestInfo,
					installed: installer.basicsInstalled,
					cloned:    installer.repoCloned,
					running:   installer.runningPid != -1,
					err:       nil,
				}
			}
			updateChan <- status.status
			status.mutex.Unlock()
		}
		newInstaller, err := NewTpuInstaller(cfg, fmt.Sprintf("%s%d", cfg.tpuPrefix, id))
		if err != nil {
			updateStatus(err)
			continue
		}
		*installer = *newInstaller

		updateStatus(nil)

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
			err = installer.InstallBasics()
			updateStatus(err)
			if err != nil {
				continue
			}
		}

		if !installer.repoCloned {
			if installer.repoClonedHash != "" {
				err = installer.KillRunningProcess()
				// process may exist, need to kill or verify it's dead
				updateStatus(err)
				fmt.Println("Killed running process")
				if err != nil {
					continue
				}
				installer.runningPid = -1
			}
			err = installer.CloneRepo()
			updateStatus(err)
			if err != nil {
				continue
			}
		}

		if installer.runningPid == -1 {
			err = installer.StartProcess()
			updateStatus(err)
		} else {
			running, err := installer.tpuController.checkProcessRunning(installer.runningPid)
			updateStatus(err)
			if err != nil {
				continue
			}
			if !running {
				err = installer.StartProcess()
				updateStatus(err)
				if err != nil {
					continue
				}
			}
		}
	}
}

func NewTpuWatcher(cfg TpuConfig) *TpuWatcher {
	tpuInstallers := make([]*TpuInstaller, cfg.numTpus)
	channel := make(chan TpuStatusUpdate)
	statuses := make([]TpuCurrentStatus, cfg.numTpus)
	for i := 0; i < cfg.numTpus; i++ {
		tpuInstallers[i] = &TpuInstaller{}
		go Watch(cfg, i, tpuInstallers[i], channel, &statuses[i])
	}
	return &TpuWatcher{
		tpuInstallers: tpuInstallers,
		updates:       channel,
		statuses:      statuses,
	}
}
