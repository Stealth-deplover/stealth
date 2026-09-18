#!/usr/bin/env bash
set -Eeuo pipefail

image="${OTEL_COLLECTOR_IMAGE:-otel/opentelemetry-collector-contrib:0.161.0}"
duration="${SMOKE_DURATION:-20s}"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/stealth-collector-log-smoke.XXXXXX")"
output_file="$(mktemp "${TMPDIR:-/tmp}/stealth-collector-log-smoke-output.XXXXXX")"
runtime_config="$temporary_dir/config.yaml"
trap 'rm -f "$output_file" "$temporary_dir/config.yaml" "$temporary_dir/runtime-config.yaml" "$temporary_dir/docker.log"; rmdir "$temporary_dir" 2>/dev/null || true' EXIT

chmod 755 "$temporary_dir"
cat >"$temporary_dir/docker.log" <<'EOF'
{"log":"hello stdout\n","stream":"stdout","time":"2026-09-18T02:12:44.123456789Z"}
{"log":"plain stderr\n","stream":"stderr","time":"2026-09-18T02:12:45.123456789Z"}
{"log":"{\"level\":\"error\",\"message\":\"structured\"}\n","stream":"stdout","time":"2026-09-18T02:12:46.123456789Z"}
{"log":"{\"severity\":\"warn\",\"message\":\"structured alias\"}\n","stream":"stdout","time":"2026-09-18T02:12:46.500Z"}
{"log":"trimmed timestamp\n","stream":"stdout","time":"2026-09-18T02:12:47Z"}
{"log":"missing severity\n","time":"2026-09-18T02:12:48Z"}
not-json-but-still-a-log-line
EOF
cat >"$temporary_dir/config.yaml" <<'EOF'
receivers:
  file_log/docker:
    include: [/test/docker.log]
    start_at: beginning
    poll_interval: 200ms
    operators:
      - type: json_parser
        id: docker-json
        parse_from: body
        parse_to: attributes
        on_error: send
      - type: move
        id: move-message
        from: attributes.log
        to: body
        on_error: send
      - type: time_parser
        id: docker-time
        parse_from: attributes.time
        layout_type: gotime
        layout: '2006-01-02T15:04:05.999999999Z07:00'
        if: 'attributes.time != nil'
        on_error: send
      - type: severity_parser
        id: stream-severity
        parse_from: attributes.stream
        mapping:
          info: stdout
          warn: stderr
        overwrite_text: true
        if: 'attributes.stream != nil'
      - type: json_parser
        id: application-json
        parse_from: body
        parse_to: attributes.application
        if: 'body matches "^\\s*\\{.*\\}\\s*$"'
        on_error: send
      - type: severity_parser
        id: application-severity
        parse_from: attributes.application.level
        overwrite_text: true
        if: 'attributes.application != nil && attributes.application.level != nil'
        on_error: send
      - type: severity_parser
        id: application-severity-alias
        parse_from: attributes.application.severity
        overwrite_text: true
        if: 'attributes.application != nil && attributes.application.level == nil && attributes.application.severity != nil'
        on_error: send
exporters:
  debug:
    verbosity: detailed
service:
  pipelines:
    logs:
      receivers: [file_log/docker]
      exporters: [debug]
EOF
chmod 644 "$temporary_dir/config.yaml" "$temporary_dir/docker.log"

set +e
if [ -n "${OTEL_COLLECTOR_BINARY:-}" ]; then
	runtime_config="$temporary_dir/runtime-config.yaml"
	sed "s#/test#$temporary_dir#g" "$temporary_dir/config.yaml" >"$runtime_config"
	timeout --signal=TERM --kill-after=5s "$duration" \
		"$OTEL_COLLECTOR_BINARY" --config="$runtime_config" >"$output_file" 2>&1
else
	timeout --signal=TERM --kill-after=5s "$duration" \
		docker run --rm --user 10001:10001 \
			-v "$temporary_dir:/test:ro" \
			"$image" --config=/test/config.yaml >"$output_file" 2>&1
fi
status=$?
set -e
if [ "$status" -ne 124 ]; then
	printf 'collector parser smoke exited with %s\n' "$status" >&2
	sed -n '1,240p' "$output_file" >&2
	exit 1
fi

for expected in "hello stdout" "plain stderr" "structured" "structured alias" "trimmed timestamp" "missing severity" "not-json-but-still-a-log-line"; do
	if ! grep -F "$expected" "$output_file" >/dev/null 2>&1; then
		printf 'collector parser smoke did not preserve %s\n' "$expected" >&2
		sed -n '1,240p' "$output_file" >&2
		exit 1
	fi
done
for expected in "SeverityText: INFO" "SeverityText: WARN" "SeverityText: ERROR" "2026-09-18T02:12:44.123456789Z" "2026-09-18T02:12:47Z"; do
	if ! grep -F "$expected" "$output_file" >/dev/null 2>&1; then
		printf 'collector parser smoke did not derive %s\n' "$expected" >&2
		sed -n '1,240p' "$output_file" >&2
		exit 1
	fi
done

printf 'collector Docker log parser smoke passed\n'
