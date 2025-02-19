package sectorstorage

import (
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/extern/sector-storage/sealtasks"
	"time"

	"context"

	"github.com/google/uuid"

	"github.com/filecoin-project/lotus/extern/sector-storage/storiface"
	"golang.org/x/xerrors"
)

func (m *Manager) WorkerStats() map[uuid.UUID]storiface.WorkerStats {
	m.sched.workersLk.RLock()
	defer m.sched.workersLk.RUnlock()

	out := map[uuid.UUID]storiface.WorkerStats{}

	// fic remoteC2
	rpcCtx, cancel := context.WithTimeout(context.TODO(), SelectorTimeout*2)
	defer cancel()
	for id, handle := range m.sched.workers {
		// fic remoteC2
		hasRemoteC2 := false
		if ok, err := handle.workerRpc.HasRemoteC2(rpcCtx); ok {
			hasRemoteC2 = true
		} else if err != nil {
			log.Errorf("get remoteC2 work info err %v:", err)
		}

		out[uuid.UUID(id)] = storiface.WorkerStats{
			Info:    handle.info,
			Enabled: handle.enabled,

			MemUsedMin: handle.active.memUsedMin,
			MemUsedMax: handle.active.memUsedMax,
			GpuUsed:    handle.active.gpuUsed,
			CpuUse:     handle.active.cpuUse,
			// fic remoteC2
			RemoteC2: hasRemoteC2,
		}
	}

	return out
}

func (m *Manager) WorkerJobs() map[uuid.UUID][]storiface.WorkerJob {
	out := map[uuid.UUID][]storiface.WorkerJob{}
	calls := map[storiface.CallID]struct{}{}

	for _, t := range m.sched.workTracker.Running() {
		out[uuid.UUID(t.worker)] = append(out[uuid.UUID(t.worker)], t.job)
		calls[t.job.ID] = struct{}{}
	}

	m.sched.workersLk.RLock()

	for id, handle := range m.sched.workers {
		handle.wndLk.Lock()
		for wi, window := range handle.activeWindows {
			for _, request := range window.todo {
				out[uuid.UUID(id)] = append(out[uuid.UUID(id)], storiface.WorkerJob{
					ID:      storiface.UndefCall,
					Sector:  request.sector.ID,
					Task:    request.taskType,
					RunWait: wi + 1,
					Start:   request.start,
				})
			}
		}
		handle.wndLk.Unlock()
	}

	m.sched.workersLk.RUnlock()

	m.workLk.Lock()
	defer m.workLk.Unlock()

	for id, work := range m.callToWork {
		_, found := calls[id]
		if found {
			continue
		}

		var ws WorkState
		if err := m.work.Get(work).Get(&ws); err != nil {
			log.Errorf("WorkerJobs: get work %s: %+v", work, err)
		}

		wait := storiface.RWRetWait
		if _, ok := m.results[work]; ok {
			wait = storiface.RWReturned
		}
		if ws.Status == wsDone {
			wait = storiface.RWRetDone
		}

		out[uuid.UUID{}] = append(out[uuid.UUID{}], storiface.WorkerJob{
			ID:       id,
			Sector:   id.Sector,
			Task:     work.Method,
			RunWait:  wait,
			Start:    time.Unix(ws.StartTime, 0),
			Hostname: ws.WorkerHostname,
		})
	}

	return out
}

func (m *Manager) GetWorker(ctx context.Context) map[string]storiface.WorkerParams {
	m.sched.workersLk.Lock()
	defer m.sched.workersLk.Unlock()

	out := map[string]storiface.WorkerParams{}

	for id, handle := range m.sched.workers {
		info := handle.workerRpc.GetWorkerInfo(ctx)
		out[id.String()] = info
	}
	return out
}

func (m *Manager) SetWorkerParam(ctx context.Context, worker, key, value string) error {
	m.sched.workersLk.Lock()
	defer m.sched.workersLk.Unlock()
	id, err := uuid.Parse(worker)
	if err != nil {
		return err
	}
	wid := WorkerID(id)
	w, exist := m.sched.workers[wid]
	if !exist {
		return xerrors.Errorf("worker not found: %s", key)
	}

	if err := w.workerRpc.SetWorkerParams(ctx, key, value); err != nil {
		return nil
	}

	if key == "group" {
		oldGroup := m.sched.workers[WorkerID(id)].info.Group
		m.sched.execGroupList.lk.Lock()
		list, exsit := m.sched.execGroupList.list[oldGroup]
		if exsit {
			for i, w := range list {
				if w == wid {
					l := append(list[:i], list[i+1:]...)
					if len(l) > 0 {
						m.sched.execGroupList.list[oldGroup] = l
					}else {
						delete(m.sched.execGroupList.list, oldGroup)
					}
					break
				}
			}
		}
		_, newExsit := m.sched.execGroupList.list[value]
		if newExsit {
			m.sched.execGroupList.list[value] = append(m.sched.execGroupList.list[value], wid)
		}else {
			newWorkerList := make([]WorkerID, 0)
			newWorkerList = append(newWorkerList, wid)
			m.sched.execGroupList.list[value] = newWorkerList
		}
		m.sched.execGroupList.lk.Unlock()
		m.sched.workers[WorkerID(id)].info.Group = value
	}
	return nil
}

func (m *Manager) UpdateSectorGroup(ctx context.Context, SectorNum string, group string) error {
	m.sched.execSectorWorker.lk.Lock()
	defer m.sched.execSectorWorker.lk.Unlock()

	sectorGroup, isExist := m.sched.execSectorWorker.group[SectorNum]
	if !isExist {
		return xerrors.Errorf("SectorID not found: %s", SectorNum)
	}
	if group == sectorGroup {
		return xerrors.Errorf("The original group is the same as the current group")
	}

	m.sched.execSectorWorker.group[SectorNum] = group
	err := m.sched.updateSectorGroupFile()
	return err
}

func (m *Manager) DeleteSectorGroup(ctx context.Context, SectorNum string) error {
	m.sched.execSectorWorker.lk.Lock()
	defer m.sched.execSectorWorker.lk.Unlock()

	delete(m.sched.execSectorWorker.group, SectorNum)

	err := m.sched.updateSectorGroupFile()
	return err
}

func (m *Manager) TrySched(ctx context.Context, group, sectorSize string) (bool, error) {
	proofSize, err := getProofSize(sectorSize)
	if err != nil {
		return false, err
	}
	m.sched.workersLk.RLock()
	defer m.sched.workersLk.RUnlock()
	sh := m.sched
	queuneLen := sh.schedQueue.Len()
	for i := 0; i < queuneLen; i++ {
		task := (*sh.schedQueue)[i]
		if group == "all" {
			if task.taskType == sealtasks.TTAddPiece {
				return false, xerrors.Errorf("schedQueue has task wait sched：%s", group)
			}
		}else {
			if task.group == group && task.taskType == sealtasks.TTAddPiece {
				return false, xerrors.Errorf("schedQueue has task wait sched：%s", group)
			}
		}
	}
	wList := make([]WorkerID, 0)
	if group == "all" {
		allList := sh.execGroupList.list
		for _, l := range allList {
			wList = append(wList, l...)
		}
	}else {
		gList, exist := sh.execGroupList.list[group]
		if exist {
			wList = append(wList, gList...)
		}
	}

	if len(wList) < 1 {
		return false, xerrors.Errorf("execGroupList not found：%s", group)
	}

	accpeWorker := make([]WorkerID, 0)
	needRes := ResourceTable[sealtasks.TTPreCommit1][proofSize]
	sel := newAllocSelector(m.index, storiface.FTSealed|storiface.FTCache, storiface.PathSealing, sealtasks.TTPreCommit1)
	for _, w := range wList {
		worker, ok := sh.workers[w]
		if !ok {
			log.Errorf("worker referenced by windowRequest not found (worker: %s)", worker)
			// TODO: How to move forward here?
			continue
		}
		if !worker.enabled {
			log.Debugw("skipping disabled worker", "worker", worker)
			continue
		}

		if !worker.active.canHandleRequest(needRes, w, "autoTask", worker.info.Resources) {
			continue
		}
		ok, err := sel.Ok(ctx, sealtasks.TTPreCommit1, proofSize, worker)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}
		accpeWorker = append(accpeWorker, w)
		break
	}
	if len(accpeWorker) < 1 {
		return false, xerrors.Errorf("can not found worker to do")
	}
	return true, nil
}

func getProofSize(s string) (abi.RegisteredSealProof, error)  {
	switch s {
	case "2KiB":
		return abi.RegisteredSealProof_StackedDrg2KiBV1, nil
	case "8MiB":
		return abi.RegisteredSealProof_StackedDrg8MiBV1, nil
	case "512MiB":
		return abi.RegisteredSealProof_StackedDrg512MiBV1, nil
	case "4GiB":
		return abi.RegisteredSealProof_StackedDrg4GiBV1, nil
	case "16GiB":
		return abi.RegisteredSealProof_StackedDrg16GiBV1, nil
	case "32GiB":
		return abi.RegisteredSealProof_StackedDrg32GiBV1, nil
	case "64GiB":
		return abi.RegisteredSealProof_StackedDrg64GiBV1, nil
	default:
		return abi.RegisteredSealProof(-1), xerrors.New("proof not found")
	}
}
