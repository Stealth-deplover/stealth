package appruntime

import (
	"context"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

func (w *Worker) processHealthCheck(parent context.Context, job repository.AppHealthCheckJob) error {
	appID := uuid.MustParse(job.App.ID)
	err := w.withHeartbeat(parent, func(ctx context.Context) error {
		return w.Store.RenewAppRuntimeLease(ctx, appID, job.WorkerID, job.LeaseToken, w.LeaseAge)
	}, func(ctx context.Context) error {
		current, err := w.Store.IsAppHealthCheckCurrent(ctx, job)
		if err != nil {
			return err
		}
		if !current {
			_ = w.Store.ReleaseAppHealthCheck(ctx, job)
			return repository.ErrAppRuntimeStale
		}
		container, found, err := w.Runtime.InspectApp(ctx, appID)
		if err != nil || !found || container.ID != job.ContainerID || !container.State.Running || containerAddress(container, w.runtimeNetworkName()) != job.Address {
			invalidateErr := w.Store.InvalidateAppHealthIdentity(ctx, job)
			if errors.Is(invalidateErr, repository.ErrAppRuntimeLeaseLost) || errors.Is(invalidateErr, repository.ErrAppRuntimeStale) {
				return invalidateErr
			}
			if invalidateErr != nil {
				return errors.Join(err, invalidateErr)
			}
			w.Logger.Warn("App runtime identity changed during health reconciliation", "app_id", appID)
			return nil
		}

		probeErr := w.Runtime.ProbeApp(ctx, job, container)
		if errors.Is(probeErr, ErrHealthRuntimeDrift) {
			if err := w.Store.InvalidateAppHealthIdentity(ctx, job); err != nil && !errors.Is(err, repository.ErrAppRuntimeStale) && !errors.Is(err, repository.ErrAppRuntimeLeaseLost) {
				return err
			}
			return nil
		}
		current, err = w.Store.IsAppHealthCheckCurrent(ctx, job)
		if err != nil {
			return err
		}
		if !current {
			_ = w.Store.ReleaseAppHealthCheck(ctx, job)
			return repository.ErrAppRuntimeStale
		}
		latest, found, inspectErr := w.Runtime.InspectApp(ctx, appID)
		if inspectErr != nil || !found || latest.ID != job.ContainerID || !latest.State.Running || containerAddress(latest, w.runtimeNetworkName()) != job.Address {
			invalidateErr := w.Store.InvalidateAppHealthIdentity(ctx, job)
			if errors.Is(invalidateErr, repository.ErrAppRuntimeLeaseLost) || errors.Is(invalidateErr, repository.ErrAppRuntimeStale) {
				return invalidateErr
			}
			if invalidateErr != nil {
				return errors.Join(inspectErr, invalidateErr)
			}
			return nil
		}
		return w.Store.CompleteAppHealthCheck(ctx, job, probeErr == nil)
	})
	if errors.Is(err, repository.ErrAppRuntimeStale) || errors.Is(err, repository.ErrAppRuntimeLeaseLost) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
