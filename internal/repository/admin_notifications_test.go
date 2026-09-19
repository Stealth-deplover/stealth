package repository

import (
	"testing"
)

func TestNormalizeAdminNotificationChannelInputRequiresProviderSafeConfiguration(t *testing.T) {
	valid := AdminNotificationChannelInput{Name: "Owner webhook", Kind: "webhook", Enabled: true, Config: []byte(`{"url":"https://alerts.example.test/hooks/1"}`)}
	if _, err := normalizeAdminNotificationChannelInput(valid); err != nil {
		t.Fatalf("valid webhook rejected: %v", err)
	}
	invalidURL := valid
	invalidURL.Config = []byte(`{"url":"http://alerts.example.test/hooks/1"}`)
	if _, err := normalizeAdminNotificationChannelInput(invalidURL); err == nil {
		t.Fatal("insecure webhook URL accepted")
	}
	invalidEmail := AdminNotificationChannelInput{Name: "Owner email", Kind: "email", Enabled: true, Config: []byte(`{"recipient":"not an email"}`)}
	if _, err := normalizeAdminNotificationChannelInput(invalidEmail); err == nil {
		t.Fatal("invalid email recipient accepted")
	}
}

func TestNormalizeAdminNotificationChannelInputRejectsControlCharacters(t *testing.T) {
	input := AdminNotificationChannelInput{Name: "Owner webhook", Kind: "webhook", Enabled: true, Config: []byte("{\"url\":\"https://alerts.example.test/hooks/1\\nsecret\"}")}
	if _, err := normalizeAdminNotificationChannelInput(input); err == nil {
		t.Fatal("control character accepted in encrypted channel configuration")
	}
}
