package sectorstorage

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	sealtasks "github.com/filecoin-project/lotus/extern/sector-storage/sealtasks"
	"github.com/filecoin-project/lotus/extern/sector-storage/stores"
)

type schedWorker struct {
	sched  *scheduler
	worker *workerHandle

	wid WorkerID

	heartbeatTimer   *time.Ticker
	scheduledWindows chan *schedWindow
	taskDone         chan struct{}

	windowsRequested int
}

// context only used for startup
func (sh *scheduler) runWorker(ctx context.Context, w Worker) error {
	info, err := w.Info(ctx)
	if err != nil {
		return xerrors.Errorf("getting worker info: %w", err)
	}

	sessID, err := w.Session(ctx)
	if err != nil {
		return xerrors.Errorf("getting worker session: %w", err)
	}
	if sessID == ClosedWorkerID {
		return xerrors.Errorf("worker already closed")
	}

	worker := &workerHandle{
		workerRpc: w,
		info:      info,

		preparing: &activeResources{},
		active:    &activeResources{},
		enabled:   true,

		closingMgr: make(chan struct{}),
		closedMgr:  make(chan struct{}),
		reqTask:    make(map[sealtasks.TaskType]uint),
	}

	wid := WorkerID(sessID)

	sh.workersLk.Lock()

	{
		_, exist := sh.execGroupList.list[info.Group]
		if exist {
			sh.execGroupList.list[info.Group] = append(sh.execGroupList.list[info.Group], wid)
		}else {
			l := make([]WorkerID, 0)
			l = append(l, wid)
			sh.execGroupList.list[info.Group] = l
		}
		worker.isDeleteGroup = false
	}

	_, exist := sh.workers[wid]
	if exist {
		log.Warnw("duplicated worker added", "id", wid)
		// this is ok, we're already handling this worker in a different goroutine
		sh.workersLk.Unlock()
		return nil
	}

	sh.workers[wid] = worker
	sh.workersLk.Unlock()

	sw := &schedWorker{
		sched:  sh,
		worker: worker,

		wid: wid,

		heartbeatTimer:   time.NewTicker(stores.HeartbeatInterval),
		scheduledWindows: make(chan *schedWindow, SchedWindows),
		taskDone:         make(chan struct{}, 1),

		windowsRequested: 0,
	}

	go sw.handleWorker()

	return nil
}

func (sw *schedWorker) handleWorker() {
	worker, sched := sw.worker, sw.sched

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	defer close(worker.closedMgr)

	defer func() {
		log.Warnw("Worker closing", "workerid", sw.wid)

		if err := sw.disable(ctx); err != nil {
			log.Warnw("failed to disable worker", "worker", sw.wid, "error", err)
		}

		sched.workersLk.Lock()
		sw.sched.deleteGroupListWithWorker(sw.worker.info.Group ,sw.wid)
		delete(sched.workers, sw.wid)
		sched.workersLk.Unlock()
	}()

	defer sw.heartbeatTimer.Stop()

	for {
		{
			sched.workersLk.Lock()
			enabled := worker.enabled
			sched.workersLk.Unlock()

			// ask for more windows if we need them (non-blocking)
			if enabled {
				if !sw.requestWindows() {
					return // graceful shutdown
				}
			}
		}

		// wait for more windows to come in, or for tasks to get finished (blocking)
		for {
			// ping the worker and check session
			if !sw.checkSession(ctx) {
				return // invalid session / exiting
			}

			// session looks good
			{
				sched.workersLk.Lock()
				enabled := worker.enabled
				worker.enabled = true
				sched.workersLk.Unlock()

				if !enabled {
					// go send window requests
					break
				}
			}

			// wait for more tasks to be assigned by the main scheduler or for the worker
			// to finish precessing a task
			update, pokeSched, ok := sw.waitForUpdates()
			if !ok {
				return
			}
			if pokeSched {
				// a task has finished preparing, which can mean that we've freed some space on some worker
				select {
				case sched.workerChange <- struct{}{}:
				default: // workerChange is buffered, and scheduling is global, so it's ok if we don't send here
				}
			}
			if update {
				break
			}
		}

		// process assigned windows (non-blocking)
		sched.workersLk.RLock()
		worker.wndLk.Lock()

		sw.workerCompactWindows()

		// send tasks to the worker
		sw.processAssignedWindows()

		worker.wndLk.Unlock()
		sched.workersLk.RUnlock()
	}
}

func (sw *schedWorker) disable(ctx context.Context) error {
	done := make(chan struct{})

	// request cleanup in the main scheduler goroutine
	select {
	case sw.sched.workerDisable <- workerDisableReq{
		activeWindows: sw.worker.activeWindows,
		wid:           sw.wid,
		done: func() {
			close(done)
		},
	}:
	case <-ctx.Done():
		return ctx.Err()
	case <-sw.sched.closing:
		return nil
	}

	// wait for cleanup to complete
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	case <-sw.sched.closing:
		return nil
	}

	sw.worker.activeWindows = sw.worker.activeWindows[:0]
	sw.windowsRequested = 0
	return nil
}

func (sw *schedWorker) checkSession(ctx context.Context) bool {
	for {
		sctx, scancel := context.WithTimeout(ctx, stores.HeartbeatInterval/2)
		curSes, err := sw.worker.workerRpc.Session(sctx)
		scancel()
		if err != nil {
			// Likely temporary error

			log.Warnw("failed to check worker session", "error", err)

			if err := sw.disable(ctx); err != nil {
				log.Warnw("failed to disable worker with session error", "worker", sw.wid, "error", err)
			}

			select {
			case <-sw.heartbeatTimer.C:
				continue
			case w := <-sw.scheduledWindows:
				// was in flight when initially disabled, return
				sw.worker.wndLk.Lock()
				sw.worker.activeWindows = append(sw.worker.activeWindows, w)
				sw.worker.wndLk.Unlock()

				if err := sw.disable(ctx); err != nil {
					log.Warnw("failed to disable worker with session error", "worker", sw.wid, "error", err)
				}
			case <-sw.sched.closing:
				return false
			case <-sw.worker.closingMgr:
				return false
			}
			continue
		}

		if WorkerID(curSes) != sw.wid {
			if curSes != ClosedWorkerID {
				// worker restarted
				log.Warnw("worker session changed (worker restarted?)", "initial", sw.wid, "current", curSes)
			}

			return false
		}

		return true
	}
}

func (sw *schedWorker) requestWindows() bool {
	for ; sw.windowsRequested < SchedWindows; sw.windowsRequested++ {
		select {
		case sw.sched.windowRequests <- &schedWindowRequest{
			worker: sw.wid,
			done:   sw.scheduledWindows,
		}:
		case <-sw.sched.closing:
			return false
		case <-sw.worker.closingMgr:
			return false
		}
	}
	return true
}

func (sw *schedWorker) waitForUpdates() (update bool, sched bool, ok bool) {
	select {
	case <-sw.heartbeatTimer.C:
		return false, false, true
	case w := <-sw.scheduledWindows:
		sw.worker.wndLk.Lock()
		sw.worker.activeWindows = append(sw.worker.activeWindows, w)
		sw.worker.wndLk.Unlock()
		return true, false, true
	case <-sw.taskDone:
		log.Debugw("task done", "workerid", sw.wid)
		return true, true, true
	case <-sw.sched.closing:
	case <-sw.worker.closingMgr:
	}

	return false, false, false
}

func (sw *schedWorker) workerCompactWindows() {
	worker := sw.worker

	// move tasks from older windows to newer windows if older windows
	// still can fit them
	if len(worker.activeWindows) > 1 {
		for wi, window := range worker.activeWindows[1:] {
			lower := worker.activeWindows[wi]
			var moved []int

			for ti, todo := range window.todo {
				needRes := ResourceTable[todo.taskType][todo.sector.ProofType]
				
				// fic remotec2
				if !todo.isRemoteC2 {
					if !lower.allocated.canHandleRequest(needRes, sw.wid, "compactWindows", worker.info.Resources) {
						continue
					}
				}

				moved = append(moved, ti)
				lower.todo = append(lower.todo, todo)

				// fic remotec2
				if !todo.isRemoteC2 {
					lower.allocated.add(worker.info.Resources, needRes)
					window.allocated.free(worker.info.Resources, needRes)
				}
			}

			if len(moved) > 0 {
				newTodo := make([]*workerRequest, 0, len(window.todo)-len(moved))
				for i, t := range window.todo {
					if len(moved) > 0 && moved[0] == i {
						moved = moved[1:]
						continue
					}

					newTodo = append(newTodo, t)
				}
				window.todo = newTodo
			}
		}
	}

	var compacted int
	var newWindows []*schedWindow

	for _, window := range worker.activeWindows {
		if len(window.todo) == 0 {
			compacted++
			continue
		}

		newWindows = append(newWindows, window)
	}

	worker.activeWindows = newWindows
	sw.windowsRequested -= compacted
}

func (sw *schedWorker) processAssignedWindows() {
	worker := sw.worker

assignLoop:
	// process windows in order
	for len(worker.activeWindows) > 0 {
		firstWindow := worker.activeWindows[0]

		// process tasks within a window, preferring tasks at lower indexes
		for len(firstWindow.todo) > 0 {
			tidx := -1

			worker.lk.Lock()
			for t, todo := range firstWindow.todo {
				needRes := ResourceTable[todo.taskType][todo.sector.ProofType]
				if worker.preparing.canHandleRequest(needRes, sw.wid, "startPreparing", worker.info.Resources) {
					tidx = t
					break
				}
			}
			worker.lk.Unlock()

			if tidx == -1 {
				break assignLoop
			}

			todo := firstWindow.todo[tidx]

			log.Debugf("assign worker sector %d", todo.sector.ID.Number)
			err := sw.startProcessingTask(sw.taskDone, todo)

			if err != nil {
				log.Errorf("startProcessingTask error: %+v", err)
				go todo.respond(xerrors.Errorf("startProcessingTask error: %w", err))
			}

			// Note: we're not freeing window.allocated resources here very much on purpose
			copy(firstWindow.todo[tidx:], firstWindow.todo[tidx+1:])
			firstWindow.todo[len(firstWindow.todo)-1] = nil
			firstWindow.todo = firstWindow.todo[:len(firstWindow.todo)-1]
		}

		copy(worker.activeWindows, worker.activeWindows[1:])
		worker.activeWindows[len(worker.activeWindows)-1] = nil
		worker.activeWindows = worker.activeWindows[:len(worker.activeWindows)-1]

		sw.windowsRequested--
	}
}

func (sw *schedWorker) startProcessingTask(taskDone chan struct{}, req *workerRequest) error {
	w, sh := sw.worker, sw.sched

	needRes := ResourceTable[req.taskType][req.sector.ProofType]

	sh.execSectorWorker.lk.Lock()
	group, isExist := sh.execSectorWorker.group[req.sector.ID.Number.String()]
	workerGroup := w.workerRpc.GetWorkerGroup(req.ctx)
	if !isExist || group != workerGroup {
		sh.execSectorWorker.group[req.sector.ID.Number.String()] = workerGroup
		err := sh.updateSectorGroupFile()
		if err != nil {
			sh.execSectorWorker.lk.Unlock()
			return err
		}
	}
	sh.execSectorWorker.lk.Unlock()

	w.lk.Lock()
	// fic remotec2
	if !req.isRemoteC2 {
		w.preparing.add(w.info.Resources, needRes)
	}
	w.lk.Unlock()

	go func() {
		// first run the prepare step (e.g. fetching sector data from other worker)
		err := req.prepare(req.ctx, sh.workTracker.worker(sw.wid, w.workerRpc))
		sh.workersLk.Lock()

		if err != nil {
			_ = w.workerRpc.DeleteStore(req.ctx, req.sector.ID, req.taskType)
			
			w.lk.Lock()
			w.preparing.free(w.info.Resources, needRes)
			w.lk.Unlock()
			sh.workersLk.Unlock()

			select {
			case taskDone <- struct{}{}:
			case <-sh.closing:
				log.Warnf("scheduler closed while sending response (prepare error: %+v)", err)
			}

			select {
			case req.ret <- workerResponse{err: err}:
			case <-req.ctx.Done():
				log.Warnf("request got cancelled before we could respond (prepare error: %+v)", err)
			case <-sh.closing:
				log.Warnf("scheduler closed while sending response (prepare error: %+v)", err)
			}
			return
		}

		// wait (if needed) for resources in the 'active' window
		// fic remotec2
		cb := func() error {
			w.lk.Lock()
			// fic remotec2
			if !req.isRemoteC2 {
				w.preparing.free(w.info.Resources, needRes)
			}
			w.lk.Unlock()
			sh.workersLk.Unlock()
			defer sh.workersLk.Lock() // we MUST return locked from this function

			select {
			case taskDone <- struct{}{}:
			case <-sh.closing:
			}

			// Do the work!
			err = req.work(req.ctx, sh.workTracker.worker(sw.wid, w.workerRpc))

			_ = w.workerRpc.DeleteStore(req.ctx, req.sector.ID, req.taskType)

			select {
			case req.ret <- workerResponse{err: err}:
			case <-req.ctx.Done():
				log.Warnf("request got cancelled before we could respond")
			case <-sh.closing:
				log.Warnf("scheduler closed while sending response")
			}

			return nil
		}

		if req.taskType == sealtasks.TTFetch {
			log.Warnf("delete taskgroup map, sectorID: %v, taskType: %s\n", req.sector, req.taskType)
			sh.execSectorWorker.lk.Lock()
			delete(sh.execSectorWorker.group, req.sector.ID.Number.String())
			_ = sh.updateSectorGroupFile()
			sh.execSectorWorker.lk.Unlock()
		}

		// fic remoteC2
		if req.isRemoteC2 {
			err = cb()
		} else {
			err = w.active.withResources(sw.wid, w.info.Resources, needRes, &sh.workersLk, cb)
		}

		sh.workersLk.Unlock()

		// This error should always be nil, since nothing is setting it, but just to be safe:
		if err != nil {
			log.Errorf("error executing worker (withResources): %+v", err)
		}
	}()

	return nil
}

func (sh *scheduler) workerCleanup(wid WorkerID, w *workerHandle) {
	select {
	case <-w.closingMgr:
	default:
		close(w.closingMgr)
	}

	sh.workersLk.Unlock()
	select {
	case <-w.closedMgr:
	case <-time.After(time.Second):
		log.Errorf("timeout closing worker manager goroutine %d", wid)
	}
	sh.workersLk.Lock()

	if !w.cleanupStarted {
		w.cleanupStarted = true

		newWindows := make([]*schedWindowRequest, 0, len(sh.openWindows))
		for _, window := range sh.openWindows {
			if window.worker != wid {
				newWindows = append(newWindows, window)
			}
		}
		sh.openWindows = newWindows

		log.Debugf("worker %s dropped", wid)
	}
}

func (sh *scheduler) deleteGroupListWithWorker(group string,wid WorkerID)  {
	if sh.workers[wid].isDeleteGroup {
		return
	}
	sh.execGroupList.lk.Lock()
	defer sh.execGroupList.lk.Unlock()
	list, exsit := sh.execGroupList.list[group]
	if !exsit {
		return
	}
	for i, w := range list {
		if w == wid {
			l := append(list[:i], list[i+1:]...)
			if len(l) > 0 {
				sh.execGroupList.list[group] = l
			}else {
				delete(sh.execGroupList.list, group)
			}
			break
		}
	}
	sh.workers[wid].isDeleteGroup = true
}
