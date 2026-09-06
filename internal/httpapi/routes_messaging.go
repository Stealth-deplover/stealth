package httpapi

import "github.com/go-chi/chi/v5"

// registerMessagingRoutes owns webhook delivery, messaging (providers,
// topics, subscribers, messages), and the authenticated realtime stream.
func (s *Server) registerMessagingRoutes(r chi.Router) {
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/webhooks", s.listWebhooks)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/webhooks", s.createWebhook)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/webhooks/{webhookID}/rotate-secret", s.rotateWebhookSecret)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/webhooks/{webhookID}/deliveries", s.listWebhookDeliveries)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/webhooks/{webhookID}", s.getWebhook)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/webhooks/{webhookID}", s.updateWebhook)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/webhooks/{webhookID}", s.deleteWebhook)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/providers", s.listMessagingProviders)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/messaging/providers", s.createMessagingProvider)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/providers/{providerID}", s.getMessagingProvider)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/messaging/providers/{providerID}", s.updateMessagingProvider)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/messaging/providers/{providerID}", s.deleteMessagingProvider)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/topics", s.listMessagingTopics)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/messaging/topics", s.createMessagingTopic)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/topics/{topicID}", s.getMessagingTopic)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/messaging/topics/{topicID}", s.updateMessagingTopic)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/messaging/topics/{topicID}", s.deleteMessagingTopic)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/topics/{topicID}/subscribers", s.listMessagingSubscribers)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/messaging/topics/{topicID}/subscribers", s.createMessagingSubscriber)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/topics/{topicID}/subscribers/{subscriberID}", s.getMessagingSubscriber)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/messaging/topics/{topicID}/subscribers/{subscriberID}", s.deleteMessagingSubscriber)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/messages", s.listMessagingMessages)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/messaging/messages", s.createMessagingMessage)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/messages/{messageID}", s.getMessagingMessage)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/messaging/messages/{messageID}/cancel", s.cancelMessagingMessage)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/messaging/messages/{messageID}/deliveries", s.listMessagingDeliveries)
	r.With(s.requireProjectDataActor).Get("/projects/{projectID}/realtime", s.realtime)
}
