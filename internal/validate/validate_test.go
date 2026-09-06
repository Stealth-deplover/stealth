package validate

import "testing"

func TestSlug(t *testing.T) {
	valid, err := Slug("My-Project", "project name")
	if err != nil || valid != "my-project" {
		t.Fatalf("got %q, %v", valid, err)
	}
	if _, err := Slug("bad_name", "project name"); err == nil {
		t.Fatal("expected invalid slug")
	}
}

func TestEmailNormalizes(t *testing.T) {
	email, err := Email("  USER@EXAMPLE.COM ")
	if err != nil || email != "user@example.com" {
		t.Fatalf("got %q, %v", email, err)
	}
}
