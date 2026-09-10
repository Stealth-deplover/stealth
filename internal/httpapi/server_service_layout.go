package httpapi

import (
	"net/http"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type projectServiceLayoutItemRequest struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	X            int    `json:"x"`
	Y            int    `json:"y"`
}

type replaceProjectServiceLayoutRequest struct {
	Layout []projectServiceLayoutItemRequest `json:"layout"`
}

func (s *Server) listProjectServiceLayout(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	items, canManage, err := s.repo.ListProjectServiceLayout(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	if projectServiceLayoutError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"layout": items, "can_manage": canManage})
}

func (s *Server) replaceProjectServiceLayout(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req replaceProjectServiceLayoutRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Layout == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "layout is required")
		return
	}
	if len(req.Layout) > repository.MaxProjectServiceLayoutItems {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "layout cannot contain more than 500 resources")
		return
	}
	items := make([]repository.ProjectServiceLayoutInput, 0, len(req.Layout))
	seen := make(map[string]struct{}, len(req.Layout))
	for _, item := range req.Layout {
		resourceType := strings.ToLower(strings.TrimSpace(item.ResourceType))
		if !validProjectServiceLayoutType(resourceType) {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "resource_type must be function, site, database, or storage")
			return
		}
		resourceID, err := repository.ParseUUID(strings.TrimSpace(item.ResourceID))
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "resource_id must be a UUID")
			return
		}
		if item.X < repository.MinProjectServiceLayoutCoord || item.X > repository.MaxProjectServiceLayoutCoord || item.Y < repository.MinProjectServiceLayoutCoord || item.Y > repository.MaxProjectServiceLayoutCoord {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "x and y must be between -100000 and 100000")
			return
		}
		key := resourceType + ":" + resourceID.String()
		if _, exists := seen[key]; exists {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "layout cannot contain duplicate resources")
			return
		}
		seen[key] = struct{}{}
		items = append(items, repository.ProjectServiceLayoutInput{ResourceType: resourceType, ResourceID: resourceID, X: item.X, Y: item.Y})
	}
	updated, err := s.repo.ReplaceProjectServiceLayout(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), items)
	if projectServiceLayoutError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"layout": updated, "can_manage": true})
}

func validProjectServiceLayoutType(resourceType string) bool {
	switch resourceType {
	case "function", "site", "database", "storage":
		return true
	default:
		return false
	}
}

func projectServiceLayoutError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case err == repository.ErrInvalidServiceLayout:
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "layout contains invalid resource data")
	case err == repository.ErrNotFound:
		writeError(w, http.StatusNotFound, "not_found", "project or service resource was not found")
	case err == repository.ErrForbidden:
		writeError(w, http.StatusForbidden, "forbidden", "only project owners and admins can change the service layout")
	default:
		return false
	}
	return true
}
