package httpapi_test

import (
	"context"
	"sync"

	"github.com/Stealth-deplover/stealth/internal/githubauth"
)

type fakeGitHubClient struct {
	mu          sync.Mutex
	device      githubauth.DeviceAuthorization
	user        githubauth.User
	accessToken string
	polls       int
}

func (f *fakeGitHubClient) RequestDeviceCode(context.Context, string) (githubauth.DeviceAuthorization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.device, nil
}

func (f *fakeGitHubClient) PollAccessToken(context.Context, string, string) (githubauth.PollResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.accessToken == "" {
		f.accessToken = "github-access-token-test-only"
	}
	return githubauth.PollResult{Status: githubauth.PollAuthorized, AccessToken: f.accessToken}, nil
}

func (f *fakeGitHubClient) GetUser(context.Context, string) (githubauth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.user, nil
}
