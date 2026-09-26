package appruntime

import (
	"context"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"time"
)

func (w *Worker) ensureStartupSweep(ctx context.Context) error {
	w.startupSweepMutex.Lock()
	defer w.startupSweepMutex.Unlock()
	if w.startSweepDone {
		return nil
	}
	if err := w.Store.ScheduleAppRuntimeStartupSweep(ctx); err != nil {
		return err
	}
	w.startSweepDone = true
	return nil
}

func (w *Worker) sweepOrphansIfDue(ctx context.Context) error {
	interval := w.OrphanSweepEvery
	if interval <= 0 {
		interval = defaultOrphanSweepEvery
	}
	if !w.lastOrphanSweep.IsZero() && time.Since(w.lastOrphanSweep) < interval {
		return nil
	}
	containers, err := w.Runtime.ListManagedAppContainers(ctx)
	if err != nil {
		return err
	}
	for _, container := range containers {
		if !managedAppContainer(container) {
			continue
		}
		appID, projectID, validIdentity := validManagedAppContainerIdentity(container)
		if !validIdentity {
			w.Logger.Warn("ignored managed App container with invalid ownership labels")
			continue
		}
		exists, err := w.Store.AppRuntimeContainerExists(ctx, projectID, appID, container.ID)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		projectCopy := projectID
		if err := w.Store.QueueAppRuntimeCleanup(ctx, &projectCopy, appID, container.ID, container.Name, 15); err != nil {
			return err
		}
		if w.Metrics != nil {
			w.Metrics.AppRuntimeOrphansQueued.Inc()
		}
	}
	w.lastOrphanSweep = time.Now()
	return nil
}

func (w *Worker) processCleanup(parent context.Context, job repository.AppRuntimeCleanupJob) error {
	started := time.Now()
	if w.Metrics != nil {
		w.Metrics.AppRuntimeCleanupInFlight.Inc()
		defer w.Metrics.AppRuntimeCleanupInFlight.Dec()
	}
	err := w.withCleanupHeartbeat(parent, job, func(ctx context.Context) error {
		return w.Runtime.RemoveCleanupTarget(ctx, job)
	})
	if err == nil {
		if err := w.Store.CompleteAppRuntimeCleanup(parent, job); err != nil {
			return err
		}
		if w.Metrics != nil {
			w.Metrics.AppRuntimeCleanupCompleted.WithLabelValues("completed").Inc()
			w.Metrics.AppRuntimeCleanupDuration.Observe(time.Since(started).Seconds())
		}
		return nil
	}
	if parent.Err() != nil || errors.Is(err, repository.ErrAppRuntimeLeaseLost) {
		return nil
	}
	terminal := errors.Is(err, ErrRuntimeOwnershipConflict) || job.AttemptCount+1 >= maxCleanupAttempts
	message := safeRuntimeError(err)
	if message == "" {
		message = "runtime cleanup unavailable"
	}
	if failErr := w.Store.FailAppRuntimeCleanup(parent, job, message, time.Now().Add(runtimeBackoff(job.AttemptCount)), terminal); failErr != nil {
		return errors.Join(err, failErr)
	}
	if w.Metrics != nil {
		result := "retry"
		if terminal {
			result = "failed"
		}
		w.Metrics.AppRuntimeCleanupCompleted.WithLabelValues(result).Inc()
		w.Metrics.AppRuntimeCleanupDuration.Observe(time.Since(started).Seconds())
	}
	return nil
}
