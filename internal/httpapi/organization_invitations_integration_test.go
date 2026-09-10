package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOrganizationInvitationIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingMailer{}
	server := httptestNewAuthServer(t, pool, recorder)
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	ownerEmail := fmt.Sprintf("invitation-owner-%s@example.test", ownerID)
	password := "correct-horse-battery-staple"
	var owner struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": ownerEmail, "password": password}, http.StatusCreated, &owner)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, owner.Account.ID, owner.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, owner.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, owner.Account.ID)
	})

	recipientClient := newIntegrationClient(t)
	recipientID := uuid.Must(uuid.NewV7())
	recipientEmail := fmt.Sprintf("invitation-recipient-%s@example.test", recipientID)
	var recipientRaw struct {
		Account struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, recipientClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": recipientEmail, "password": password}, http.StatusCreated, &recipientRaw)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, recipientRaw.Account.ID, recipientRaw.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, recipientRaw.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, recipientRaw.Account.ID)
	})

	var created struct {
		Invitation struct {
			ID     string `json:"id"`
			Email  string `json:"email"`
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"invitation"`
		Delivery string `json:"delivery"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+owner.Organization.ID+"/invitations", map[string]string{"email": recipientEmail, "role": "developer"}, http.StatusCreated, &created)
	if created.Invitation.Email != recipientEmail || created.Invitation.Role != "developer" || created.Invitation.Status != "pending" || created.Delivery != "sent" {
		t.Fatalf("created invitation = %+v", created)
	}
	message, ok := recorder.latestFor("You are invited to a Stealth organization", "")
	if !ok {
		t.Fatal("invitation email was not recorded")
	}
	token := messageLink(message.TextBody).Query().Get("token")
	if token == "" {
		t.Fatal("invitation email did not contain a tokenized link")
	}

	var listed struct {
		Invitations []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"invitations"`
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/organizations/"+owner.Organization.ID+"/invitations", nil, http.StatusOK, &listed)
	if !listed.CanManage || len(listed.Invitations) != 1 || listed.Invitations[0].ID != created.Invitation.ID || listed.Invitations[0].Status != "pending" {
		t.Fatalf("listed invitations = %+v", listed)
	}

	var accepted struct {
		Membership struct {
			OrganizationID string `json:"organization_id"`
			AccountID      string `json:"account_id"`
			Role           string `json:"role"`
		} `json:"membership"`
	}
	requestJSON(t, recipientClient, http.MethodPost, server.URL+"/v1/organization-invitations/accept", map[string]string{"token": token}, http.StatusOK, &accepted)
	if accepted.Membership.OrganizationID != owner.Organization.ID || accepted.Membership.AccountID != recipientRaw.Account.ID || accepted.Membership.Role != "developer" {
		t.Fatalf("accepted membership = %+v", accepted.Membership)
	}
	requestJSON(t, recipientClient, http.MethodPost, server.URL+"/v1/organization-invitations/accept", map[string]string{"token": token}, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/organizations/"+owner.Organization.ID+"/invitations", nil, http.StatusOK, &listed)
	if len(listed.Invitations) != 0 {
		t.Fatalf("accepted invitation remained pending: %+v", listed.Invitations)
	}
}
