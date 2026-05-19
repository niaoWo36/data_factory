package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"data_factory/internal/config"
	"data_factory/internal/db"
)

// MigrateTask tracks a running migration job.
type MigrateTask struct {
	ID       string    `json:"id"`
	Status   string    `json:"status"` // running | done | error
	StartAt  time.Time `json:"start_at"`
	Message  string    `json:"message"`
	cancel   context.CancelFunc
	progress chan db.Progress
}

var taskCounter int64

// handleMigrateStart starts a background migration task and returns its ID.
func (s *Server) handleMigrateStart(w http.ResponseWriter, r *http.Request) {
	var opts config.MigrateOptions
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	opts.Config.SameDB = db.IsSameDB(opts.Config)

	taskID := fmt.Sprintf("task_%d_%s", atomic.AddInt64(&taskCounter, 1), timestamp())
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)

	task := &MigrateTask{
		ID:       taskID,
		Status:   "running",
		StartAt:  time.Now(),
		cancel:   cancel,
		progress: make(chan db.Progress, 256),
	}
	s.tasks.Store(taskID, task)

	go s.runMigration(ctx, task, opts)

	log.Printf("migration task %s queued: same_db=%v tenant_ids=%v schema=%v data=%v timeseries=%v src=%s/%s dst=%s/%s",
		taskID, opts.Config.SameDB, opts.TenantIDs, opts.MigrateSchema, opts.MigrateData, opts.MigrateTimeSeries,
		opts.Config.SrcMain.DBName, db.SchemaOf(opts.Config.SrcMain),
		opts.Config.DstMain.DBName, db.SchemaOf(opts.Config.DstMain))
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": taskID})
}

// handleMigrateProgress upgrades the connection to a WebSocket and streams progress
// events for a given task_id query parameter.
func (s *Server) handleMigrateProgress(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task_id required")
		return
	}
	v, ok := s.tasks.Load(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	task := v.(*MigrateTask)

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	for p := range task.progress {
		if err := conn.WriteJSON(p); err != nil {
			break
		}
	}
	// Send final status.
	conn.WriteJSON(map[string]string{"stage": "done", "message": task.Message, "status": task.Status})
}

// handleMigrateStatus returns the current status of a task.
func (s *Server) handleMigrateStatus(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	v, ok := s.tasks.Load(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	task := v.(*MigrateTask)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       task.ID,
		"status":   task.Status,
		"start_at": task.StartAt,
		"message":  task.Message,
	})
}

// runMigration performs the actual migration in a goroutine.
func (s *Server) runMigration(ctx context.Context, task *MigrateTask, opts config.MigrateOptions) {
	defer func() {
		task.cancel()
		close(task.progress)
	}()

	emit := func(p db.Progress) {
		if p.Error != "" {
			log.Printf("migration task %s [%s][%s] error=%s", task.ID, p.Stage, p.Table, p.Error)
		} else if p.Message != "" {
			log.Printf("migration task %s [%s][%s] %s", task.ID, p.Stage, p.Table, p.Message)
		}
		select {
		case task.progress <- p:
		default:
		}
	}

	cfg := opts.Config
	log.Printf("migration task %s started with tenant_ids=%v", task.ID, opts.TenantIDs)

	// Open connections.
	srcMain, err := db.OpenSrcMain(cfg)
	if err != nil {
		task.Status = "error"
		task.Message = "connect src_main: " + err.Error()
		emit(db.Progress{Stage: "error", Error: task.Message})
		return
	}
	defer srcMain.Close()

	dstMain, err := db.OpenDstMain(cfg)
	if err != nil {
		task.Status = "error"
		task.Message = "connect dst_main: " + err.Error()
		emit(db.Progress{Stage: "error", Error: task.Message})
		return
	}
	defer dstMain.Close()

	srcSchema := db.SchemaOf(cfg.SrcMain)
	dstSchema := db.SchemaOf(cfg.DstMain)

	// 1. Schema migration.
	if opts.MigrateSchema {
		emit(db.Progress{Stage: "schema", Message: "Starting schema migration..."})
		if err := db.MigrateSchema(ctx, srcMain, dstMain, srcSchema, dstSchema, emit); err != nil {
			task.Status = "error"
			task.Message = "schema: " + err.Error()
			emit(db.Progress{Stage: "error", Error: task.Message})
			return
		}
	}

	// 2. Data migration.
	if opts.MigrateData {
		emit(db.Progress{Stage: "data", Message: "Starting data migration..."})
		if err := db.MigrateData(ctx, srcMain, dstMain, srcSchema, dstSchema,
			opts.TenantIDs, cfg.SameDB, emit); err != nil {
			task.Status = "error"
			task.Message = "data: " + err.Error()
			emit(db.Progress{Stage: "error", Error: task.Message})
			return
		}
	}

	// 3. Time-series migration.
	if opts.MigrateTimeSeries {
		srcTS, err := db.OpenSrcTS(cfg)
		if err != nil {
			task.Status = "error"
			task.Message = "connect src_ts: " + err.Error()
			emit(db.Progress{Stage: "error", Error: task.Message})
			return
		}
		defer srcTS.Close()

		dstTS, err := db.OpenDstTS(cfg)
		if err != nil {
			task.Status = "error"
			task.Message = "connect dst_ts: " + err.Error()
			emit(db.Progress{Stage: "error", Error: task.Message})
			return
		}
		defer dstTS.Close()

		emit(db.Progress{Stage: "timeseries", Message: "Starting time-series migration..."})
		if err := db.MigrateTimeSeries(ctx, srcTS, dstTS, srcMain,
			db.SchemaOf(cfg.SrcTS), db.SchemaOf(cfg.DstTS), srcSchema,
			opts.TenantIDs, cfg.SameDB, emit); err != nil {
			task.Status = "error"
			task.Message = "timeseries: " + err.Error()
			emit(db.Progress{Stage: "error", Error: task.Message})
			return
		}
	}

	task.Status = "done"
	task.Message = "Migration completed successfully"
	log.Printf("migration task %s completed successfully", task.ID)
	emit(db.Progress{Stage: "done", Message: task.Message})
}
