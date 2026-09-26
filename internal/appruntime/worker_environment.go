package appruntime

import (
	"bytes"
	"context"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type RuntimeEnvironmentVariable struct {
	Key   string
	Value []byte
}

func (w *Worker) runtimeEnvironment(ctx context.Context, job repository.AppRuntimeJob) ([]RuntimeEnvironmentVariable, error) {
	encrypted, err := w.Store.ListAppRuntimeEnvironment(ctx, job)
	if err != nil {
		return nil, err
	}
	if len(encrypted) > repository.AppEnvironmentVariableMaxCount {
		return nil, ErrAppEnvironmentDecryption
	}
	if len(encrypted) == 0 {
		return nil, nil
	}
	if w.AppSecretsCipher == nil {
		return nil, ErrAppEnvironmentDecryption
	}
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	appID, appErr := uuid.Parse(job.App.ID)
	if projectErr != nil || appErr != nil || projectID == uuid.Nil || appID == uuid.Nil {
		return nil, ErrAppEnvironmentDecryption
	}
	values := make([]RuntimeEnvironmentVariable, 0, len(encrypted))
	seen := make(map[string]struct{}, len(encrypted))
	totalValueBytes := 0
	for _, item := range encrypted {
		if item.ID == uuid.Nil || !appRuntimeEnvironmentKey.MatchString(item.Key) {
			wipeRuntimeEnvironment(values)
			return nil, ErrAppEnvironmentDecryption
		}
		if _, duplicate := seen[item.Key]; duplicate {
			wipeRuntimeEnvironment(values)
			return nil, ErrAppEnvironmentDecryption
		}
		seen[item.Key] = struct{}{}
		plaintext, decryptErr := w.AppSecretsCipher.Decrypt(projectID, appID, item.ID, item.Ciphertext)
		if decryptErr != nil || len(plaintext) > repository.AppEnvironmentVariableMaxValueBytes || bytes.IndexAny(plaintext, "\x00\r\n") >= 0 {
			clear(plaintext)
			wipeRuntimeEnvironment(values)
			return nil, ErrAppEnvironmentDecryption
		}
		if len(plaintext) > repository.AppEnvironmentVariableMaxTotalValueBytes-totalValueBytes {
			clear(plaintext)
			wipeRuntimeEnvironment(values)
			return nil, ErrAppEnvironmentDecryption
		}
		totalValueBytes += len(plaintext)
		values = append(values, RuntimeEnvironmentVariable{Key: item.Key, Value: plaintext})
	}
	return values, nil
}

func wipeRuntimeEnvironment(values []RuntimeEnvironmentVariable) {
	for index := range values {
		clear(values[index].Value)
		values[index].Value = nil
	}
}
