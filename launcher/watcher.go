package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
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

func debugprintf(format string, a ...any) {
	debugFile, err := os.OpenFile("./debug.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("error opening debug file: %v\n", err)
		return
	}
	defer debugFile.Close()
	fmt.Fprintf(debugFile, format, a...)
	debugFile.Sync()
}

type Synchronizer struct {
	chans         []chan any
	alreadyLocked int
	lock          sync.Mutex
}

func (s *Synchronizer) Add(n int) {
	for i := 0; i < n; i++ {
		s.chans = append(s.chans, make(chan any))
	}
}

func (s *Synchronizer) getIndex() int {
	s.lock.Lock()
	myIndex := s.alreadyLocked
	s.alreadyLocked++
	s.lock.Unlock()
	return myIndex
}

func (s *Synchronizer) Sync() int {
	myIndex := s.getIndex()
	writeToIndex := (myIndex + 1) % len(s.chans)
	go func() {
		s.chans[writeToIndex] <- struct{}{}
	}()
	<-s.chans[myIndex]
	s.lock.Lock()
	s.alreadyLocked = 0
	s.lock.Unlock()
	go func() {
		s.chans[writeToIndex] <- struct{}{}
	}()
	<-s.chans[myIndex]
	return myIndex
}

func (s *Synchronizer) AllGather(value any) []any {
	myIndex := s.Sync()
	for i := 0; i < len(s.chans); i++ {
		if i != myIndex {
			go func() {
				s.chans[i] <- value
			}()
		}
	}
	results := make([]any, len(s.chans))
	results[0] = value
	for i := 0; i < len(s.chans)-1; i++ {
		results[i+1] = <-s.chans[myIndex]
	}
	return results
}

func Watch(cfg TpuConfig, id int, installer *TpuInstaller, updateChan chan TpuStatusUpdate, statuses *[]TpuCurrentStatus, groupWg *sync.WaitGroup, activeSynchronizer *Synchronizer, currentGroupId *atomic.Int32) {
	firstIteration := true
	for {
		if !firstIteration {
			time.Sleep(5 * time.Second)
		}
		firstIteration = false
		status := &(*statuses)[id]
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
			case tpuStatusStopped:
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
				if err != nil {
					continue
				}
				installer.runningPid = -1
				installer.repoClonedHash = ""
			}
			err = installer.CloneRepo()
			updateStatus(err)
			if err != nil {
				continue
			}
		}

		lockedMutexes := make([]*sync.Mutex, 0)
		LockAll := func() {
			lockedMutexes = make([]*sync.Mutex, 0)
			for i := range len(*statuses) {
				lockedMutexes = append(lockedMutexes, &(*statuses)[i].mutex)
				(*statuses)[i].mutex.Lock()
			}
		}
		UnlockAll := func() {
			for _, mutex := range lockedMutexes {
				mutex.Unlock()
			}
			lockedMutexes = nil
		}

		// we only ever lock mutexes one at a time for a brief period so this is fine
		{
			LockAll()
			numActive := 0
			for i := range len(*statuses) {
				status := (*statuses)[i].status
				if status.status == tpuStatusRunning && status.installed && status.cloned {
					numActive++
				}
			}
			UnlockAll()
			if numActive < cfg.numTpusActive {
				continue
			}
		}

		// we only ever manage the running TPUs if we get a sufficient number of them up.
		// we may adjust numTpusActive, but we still don't want to manage 1-2 alive TPUs,
		// before the others are ready.

		// we want to synchronize the running TPUs once they are all up.
		// we do this by waiting for the groupWg to be done.
		// now, all running TPUs are guaranteed to execute the code below.
		groupWg.Done()
		groupWg.Wait()

		// useful primitive. i should have used it more
		barrier := func() {
			activeSynchronizer.Sync()
		}
		checkErr := func(err error) error {
			activeSynchronizer.Sync()
			errors := activeSynchronizer.AllGather(err)
			var realErr error = nil
			for _, err := range errors {
				if err != nil && err != struct{}{} {
					realErr = err.(error)
					break
				}
			}
			if realErr != nil {
				activeSynchronizer.Sync()
				return realErr
			}
			activeSynchronizer.Sync()
			return nil
		}

		barrier()

		groupWg.Add(1)

		barrier()

		var loadedGroupId int32

		firstInnerIteration := true
		for {
			if !firstInnerIteration {
				time.Sleep(5 * time.Second)
			}
			firstInnerIteration = false

			debugprintf("rank %d, first inner iteration: %v\n", id, firstInnerIteration)

			{
				barrier()
				loadedGroupId = currentGroupId.Load()
				// 	barrier()
				// 	err := checkErr(installer.UpdateStatus())
				// 	updateStatus(err)
				// 	if err != nil {
				// 		continue
				// 	}
				barrier()
			}

			debugprintf("rank %d, second inner iteration: %v\n", id, firstInnerIteration)

			{
				barrier()

				// check if all TPUs are still running. if some are not, we exit the active group.
				// for this block, all active TPUs should have the same state.
				// TODO check the actual set of running TPUs instead of the number
				numNotAlive := 0
				{
					LockAll()
					for i := range len(*statuses) {
						status := (*statuses)[i].status
						if status.status != tpuStatusRunning || !status.installed || !status.cloned {
							numNotAlive++
						}
					}
					UnlockAll()
				}
				if numNotAlive > 0 {
					activeSynchronizer.Sync()
					break
				}
			}
			barrier()

			debugprintf("rank %d, third inner iteration: %v\n", id, firstInnerIteration)

			{
				// if we have a current group id, do a health check. check all processes are running
				// if some are running, but not all, kill the ones that are running.
				if loadedGroupId > 0 {
					numRunning := 0
					{
						LockAll()
						for i := range len(*statuses) {
							status := (*statuses)[i].status
							if status.status == tpuStatusRunning && status.installed && status.cloned && status.running {
								numRunning++
							}
						}
						UnlockAll()
					}
					if numRunning < cfg.numTpusActive {
						barrier()
						// kill the ones that are running
						// we do this by setting the group id to 0, the next iteration will kill all running processes
						currentGroupId.Store(0)
						loadedGroupId = 0
						continue
					}
				} else {
					debugprintf("rank %d, fourth inner iteration: %v\n", id, firstInnerIteration)
					// if no TPUs have a running PID, we create a new group id and start all processes together.
					numNotRunning := 0
					{
						LockAll()
						for i := range len(*statuses) {
							status := (*statuses)[i].status
							if status.status == tpuStatusRunning && status.installed && status.cloned && !status.running {
								numNotRunning++
							}
						}
						UnlockAll()
					}
					debugprintf("rank %d, fifth inner iteration: %v %d\n", id, firstInnerIteration, numNotRunning)
					if numNotRunning >= cfg.numTpusActive {
						// all TPUs are not running. we can create a new group id.
						barrier()
						currentGroupId.Store(int32(rand.IntN(1000000) + 1))
						barrier()
						attemptedGroupId := currentGroupId.Load()
						barrier()
						currentGroupId.Store(0)
						myIndex := activeSynchronizer.Sync()
						debugprintf("rank %d, my index: %d, creating new group id: %d\n", id, myIndex, attemptedGroupId)
						myPorts, err := installer.GetUnusedPorts(cfg.numTpusActive - 1)
						err = checkErr(err)
						updateStatus(err)
						if err != nil {
							continue
						}
						debugprintf("rank %d, my index: %d, my ports: %v\n", id, myIndex, myPorts)
						barrier()
						allPortsRaw := activeSynchronizer.AllGather(myPorts)
						barrier()
						allPorts := make([][]int, 0)
						for _, raw := range allPortsRaw {
							allPorts = append(allPorts, raw.([]int))
						}
						debugprintf("rank %d, all ports: %v\n", id, allPorts)
						barrier()
						myHost := make([][]any, len(myPorts))
						for i, port := range myPorts {
							myHost[i] = []any{installer.tpuController.latestInfo.IP, port}
						}
						allHostsRaw := activeSynchronizer.AllGather(myHost)
						allHosts := make([][]any, len(allHostsRaw))
						for i, raw := range allHostsRaw {
							allHosts[i] = raw.([]any)
						}
						otherHosts := make([]any, len(allHosts)-1)
						for i := range len(allHosts) {
							if i == 0 {
								continue
							}
							otherHosts[i-1] = allHosts[i][myIndex]
						}
						barrier()
						err = installer.WriteRaleighInfo(raleighInfo{
							Ports:   myPorts,
							GroupId: int(attemptedGroupId),
							Seed:    myPorts[0],
							Hosts:   otherHosts,
						})
						err = checkErr(err)
						updateStatus(err)
						if err != nil {
							continue
						}
						barrier()
						currentGroupId.Store(attemptedGroupId)
						barrier()
						err = installer.StartProcess()
						err = checkErr(err)
						updateStatus(err)
						if err != nil {
							continue
						}
						continue
					} else {
						barrier()
						// we need to kill some of the running processes
						// specifically, we kill the process on the TPU we own.
						err := installer.KillRunningProcess()
						err = checkErr(err)
						updateStatus(err)
						if err != nil {
							continue
						}
						installer.runningPid = -1
						installer.repoClonedHash = ""
						continue
					}
				}
			}
		}
	}
}

func NewTpuWatcher(cfg TpuConfig) *TpuWatcher {
	tpuInstallers := make([]*TpuInstaller, cfg.numTpus)
	channel := make(chan TpuStatusUpdate)
	statuses := make([]TpuCurrentStatus, cfg.numTpus)
	groupWg := sync.WaitGroup{}
	activeSynchronizer := Synchronizer{}
	activeSynchronizer.Add(cfg.numTpusActive)
	groupWg.Add(cfg.numTpusActive)
	currentGroupId := atomic.Int32{}
	currentGroupId.Store(0) // TODO load from one of the active TPUs
	for i := 0; i < cfg.numTpus; i++ {
		tpuInstallers[i] = &TpuInstaller{}
		go Watch(cfg, i, tpuInstallers[i], channel, &statuses, &groupWg, &activeSynchronizer, &currentGroupId)
	}
	return &TpuWatcher{
		tpuInstallers: tpuInstallers,
		updates:       channel,
		statuses:      statuses,
	}
}
