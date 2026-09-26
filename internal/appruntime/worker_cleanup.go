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
	if interval < time.Minute || interval > 24*time.Hour {
		interval = defaultOrphanSweepEvery
	}
	now := time.Now()
	if !w.nextOrphanSweep.IsZero() && now.Before(w.nextOrphanSweep) {
		return nil
	}
	containers, err := w.Runtime.ListManagedAppContainers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.deferOrphanSweep(time.Now(), interval)
		}
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
			if ctx.Err() == nil {
				w.deferOrphanSweep(time.Now(), interval)
			}
			return err
		}
		if exists {
			continue
		}
		projectCopy := projectID
		if err := w.Store.QueueAppRuntimeCleanup(ctx, &projectCopy, appID, container.ID, container.Name, 15); err != nil {
			if ctx.Err() == nil {
				w.deferOrphanSweep(time.Now(), interval)
			}
			return err
		}
		if w.Metrics != nil {
			w.Metrics.AppRuntimeOrphansQueued.Inc()
		}
	}
	w.orphanFailureCount = 0
	w.nextOrphanSweep = time.Now().Add(interval)
	return nil
}

func (w *Worker) deferOrphanSweep(now time.Time, interval time.Duration) {
	if w.orphanFailureCount < 32 {
		w.orphanFailureCount++
	}
	w.nextOrphanSweep = now.Add(boundedMaintenanceBackoff(interval, w.orphanFailureCount, maxOrphanSweepBackoff))
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
