package httpapi

import "github.com/go-chi/chi/v5"

func (s *Server) registerStorageRoutes(r chi.Router) {
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/storage/buckets", s.listStorageBuckets)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/storage/buckets", s.createStorageBucket)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/storage/buckets/{bucketID}", s.getStorageBucket)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/storage/buckets/{bucketID}", s.updateStorageBucket)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/storage/buckets/{bucketID}", s.deleteStorageBucket)
	r.With(s.requireProjectStorageActor).Get("/projects/{projectID}/storage/buckets/{bucketID}/files", s.listStorageFiles)
	r.With(s.requireProjectStorageActor).Post("/projects/{projectID}/storage/buckets/{bucketID}/files", s.uploadStorageFile)
	r.With(s.requireProjectStorageActor).Get("/projects/{projectID}/storage/buckets/{bucketID}/files/{fileID}", s.getStorageFile)
	r.With(s.requireProjectStorageActor).Patch("/projects/{projectID}/storage/buckets/{bucketID}/files/{fileID}", s.updateStorageFile)
	r.With(s.requireProjectStorageActor).Get("/projects/{projectID}/storage/buckets/{bucketID}/files/{fileID}/download", s.downloadStorageFile)
	r.With(s.requireProjectStorageActor).Delete("/projects/{projectID}/storage/buckets/{bucketID}/files/{fileID}", s.deleteStorageFile)
}
