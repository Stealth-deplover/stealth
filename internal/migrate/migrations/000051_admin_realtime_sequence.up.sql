-- Serialize delivery-cursor allocation in the producer transaction while
-- keeping the UUID as the event's identity. The transaction-scoped advisory
-- lock used by the repository keeps a transaction that allocated an earlier
-- sequence from being overtaken by a later committed event.
CREATE SEQUENCE admin_realtime_event_sequence AS BIGINT;

ALTER TABLE admin_realtime_events
  ADD COLUMN sequence BIGINT;

WITH ordered_events AS (
  SELECT id,
         row_number() OVER (ORDER BY occurred_at ASC, id ASC)::BIGINT AS sequence
  FROM admin_realtime_events
)
UPDATE admin_realtime_events AS events
SET sequence = ordered_events.sequence
FROM ordered_events
WHERE events.id = ordered_events.id;

SELECT setval(
  'admin_realtime_event_sequence'::regclass,
  COALESCE((SELECT MAX(sequence) FROM admin_realtime_events), 1),
  EXISTS (SELECT 1 FROM admin_realtime_events)
);

ALTER TABLE admin_realtime_events
  ALTER COLUMN sequence SET DEFAULT nextval('admin_realtime_event_sequence'::regclass),
  ALTER COLUMN sequence SET NOT NULL,
  ADD CONSTRAINT admin_realtime_events_sequence_unique UNIQUE (sequence);

ALTER SEQUENCE admin_realtime_event_sequence
  OWNED BY admin_realtime_events.sequence;
