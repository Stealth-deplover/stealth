package installengine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRenderTraefikCoreAssetUsesConfiguredHostname(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repoRootForTraefikTest(t), "traefik", "dynamic", "core.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderTraefikCoreAsset(contents, Plan{PublicURL: "https://console.example.test:8443"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	if strings.Contains(text, traefikPublicHostPlaceholder) || !strings.Contains(text, "Host(`console.example.test`)") {
		t.Fatalf("rendered core routes = %q", text)
	}
	if err := validateTraefikCoreAsset(rendered); err != nil {
		t.Fatalf("rendered core validation failed: %v", err)
	}
}

func TestRenderTraefikCoreAssetReadsExistingConfig(t *testing.T) {
	root := t.TempDir()
	layout, err := NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(layout.EnvFile, "PUBLIC_APP_URL=http://127.0.0.1:13000\n"); err != nil {
		t.Fatal(err)
	}
	rendered, err := renderTraefikCoreAsset([]byte("rule: Host(`__STEALTH_PUBLIC_HOST__`)\n"), Plan{Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != "rule: Host(`127.0.0.1`)\n" {
		t.Fatalf("rendered host = %q", rendered)
	}
}

func TestTraefikReleaseConfigKeepsProviderAndNetworkBoundaries(t *testing.T) {
	root := repoRootForTraefikTest(t)
	static, err := os.ReadFile(filepath.Join(root, "traefik", "traefik.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := GenerateConfig(ConfigOptions{Version: "v1.2.3", PublicURL: "https://console.example.test", GitHubAppClientID: "Iv1.test-client-id"})
	if err != nil {
		t.Fatal(err)
	}
	renderedStatic, err := renderTraefikStaticAsset(static, Plan{ConfigContents: config})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTraefikStaticAsset(renderedStatic); err != nil {
		t.Fatal(err)
	}
	staticText := string(renderedStatic)
	for _, forbidden := range []string{"docker:", "/var/run/docker.sock", "dashboard: true", "forwardedHeaders.insecure: true"} {
		if strings.Contains(staticText, forbidden) {
			t.Fatalf("static Traefik config contains forbidden %q", forbidden)
		}
	}
	compose, err := os.ReadFile(filepath.Join(root, "compose.production.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	composeText := string(compose)
	for _, required := range []string{
		"  traefik:",
		"./traefik/traefik.yaml:/etc/traefik/traefik.yaml:ro",
		"./traefik/dynamic:/etc/traefik/dynamic:ro",
		"cap_drop: [ALL]",
		"no-new-privileges:true",
		"stealth_ingress:",
		"internal: true",
	} {
		if !strings.Contains(composeText, required) {
			t.Fatalf("production Compose is missing Traefik boundary marker %q", required)
		}
	}
	traefikStart := strings.Index(composeText, "\n  traefik:")
	if traefikStart < 0 {
		t.Fatal("Traefik service is missing")
	}
	traefikEnd := strings.Index(composeText[traefikStart+1:], "\n  cloudflared:")
	if traefikEnd < 0 {
		t.Fatal("could not delimit Traefik service")
	}
	traefikBlock := composeText[traefikStart : traefikStart+1+traefikEnd]
	for _, forbidden := range []string{"/var/run/docker.sock", "privileged:", "network_mode: host", "/hostfs", "cap_add:"} {
		if strings.Contains(traefikBlock, forbidden) {
			t.Fatalf("Traefik service contains forbidden marker %q", forbidden)
		}
	}
}

func TestGenerateConfigPinsTraefikAndTrustedIngressPeer(t *testing.T) {
	config, err := GenerateConfig(ConfigOptions{
		Version:           "v1.2.3",
		PublicURL:         "https://console.example.test",
		GitHubAppClientID: "Iv1.test-client-id",
		DockerGID:         42,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"TRAEFIK_IMAGE=" + defaultTraefikImage,
		"STEALTH_INGRESS_NETWORK_NAME=stealth_ingress",
		"STEALTH_INGRESS_NETWORK_SUBNET=172.31.0.0/24",
		"STEALTH_INGRESS_IP_RANGE=172.31.0.64/26",
		"STEALTH_TRAEFIK_INGRESS_IP=172.31.0.254",
		"STEALTH_CLOUDFLARED_INGRESS_IP=172.31.0.10",
		"TRUSTED_PROXY_CIDRS=172.30.0.0/24,172.31.0.254/32",
	} {
		if !strings.Contains(config, marker) {
			t.Fatalf("generated config is missing %q", marker)
		}
	}
}

func TestGenerateConfigPersistsConfiguredIngressNetworkName(t *testing.T) {
	config, err := GenerateConfig(ConfigOptions{
		Version:            "v1.2.3",
		PublicURL:          "https://console.example.test",
		GitHubAppClientID:  "Iv1.test-client-id",
		IngressNetworkName: "stealth_b_ingress",
	})
	if err != nil {
		t.Fatal(err)
	}
	values, err := ParseEnvContents(config)
	if err != nil {
		t.Fatal(err)
	}
	if values["STEALTH_INGRESS_NETWORK_NAME"] != "stealth_b_ingress" {
		t.Fatalf("generated ingress network name = %q, want custom name", values["STEALTH_INGRESS_NETWORK_NAME"])
	}
}

func TestTraefikConfigValidationRejectsMalformedAndDanglingRoutes(t *testing.T) {
	if err := validateTraefikStaticAsset([]byte("entryPoints: [")); err == nil {
		t.Fatal("malformed static YAML was accepted")
	}
	template := []byte("http:\n  routers:\n    stealth-api:\n      entryPoints: [web]\n      rule: Host(`__STEALTH_PUBLIC_HOST__`)\n      service: missing\n    stealth-console:\n      entryPoints: [web]\n      rule: Host(`__STEALTH_PUBLIC_HOST__`)\n      service: stealth-console\n  services:\n    stealth-console:\n      loadBalancer:\n        passHostHeader: true\n        servers:\n          - url: http://console:3000\n")
	rendered, err := renderTraefikCoreAsset(template, Plan{PublicURL: "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTraefikCoreAsset(rendered); err == nil {
		t.Fatal("core route with missing service reference was accepted")
	}
	valid, err := os.ReadFile(filepath.Join(repoRootForTraefikTest(t), "traefik", "dynamic", "core.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err = renderTraefikCoreAsset(valid, Plan{PublicURL: "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	invalidRouter := strings.ReplaceAll(string(rendered), "Host(`example.test`)", "\"\"")
	if err := validateTraefikCoreAsset([]byte(invalidRouter)); err == nil {
		t.Fatal("core route with an empty rule was accepted")
	}
}

func TestMigrateReleaseConfigAddsTraefikPeerToCustomTrustedProxies(t *testing.T) {
	contents, err := MigrateReleaseConfig(map[string]string{
		"TRUSTED_PROXY_CIDRS": "10.0.0.0/8, 192.0.2.0/24",
	}, "v1.2.4", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(t.TempDir(), "config.env")
	if err := os.WriteFile(envPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := ReadEnvFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := values["TRUSTED_PROXY_CIDRS"]; got != "10.0.0.0/8, 192.0.2.0/24,172.31.0.254/32" {
		t.Fatalf("migrated trusted proxies = %q", got)
	}
}

func TestMigrateReleaseConfigUsesPersistedTraefikPeer(t *testing.T) {
	contents, err := MigrateReleaseConfig(map[string]string{
		"STEALTH_INGRESS_NETWORK_NAME":   "operator_ingress",
		"STEALTH_INGRESS_NETWORK_SUBNET": "10.44.8.0/24",
		"STEALTH_INGRESS_IP_RANGE":       "10.44.8.64/26",
		"STEALTH_TRAEFIK_INGRESS_IP":     "10.44.8.254",
		"STEALTH_CLOUDFLARED_INGRESS_IP": "10.44.8.10",
		"TRUSTED_PROXY_CIDRS":            "172.30.0.0/24",
	}, "v1.2.4", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	values, err := ParseEnvContents(contents)
	if err != nil {
		t.Fatal(err)
	}
	if got := values["TRUSTED_PROXY_CIDRS"]; got != "172.30.0.0/24,10.44.8.254/32" {
		t.Fatalf("migrated trusted proxies = %q", got)
	}
}

func TestTraefikStaticAssetRendersConfiguredCloudflaredPeer(t *testing.T) {
	contents := []byte(testTraefikStaticAsset())
	config, err := GenerateConfig(ConfigOptions{
		Version:              "v1.2.3",
		PublicURL:            "https://console.example.test",
		GitHubAppClientID:    "Iv1.test-client-id",
		IngressNetworkSubnet: "172.31.7.0/24",
		TraefikIngressIP:     "172.31.7.254",
		CloudflaredIngressIP: "172.31.7.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderTraefikStaticAsset(contents, Plan{ConfigContents: config})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), traefikCloudflaredTrustedCIDRPlaceholder) || !strings.Contains(string(rendered), "172.31.7.10/32") {
		t.Fatalf("rendered trusted peer = %q", rendered)
	}
	if err := validateTraefikStaticAsset(rendered); err != nil {
		t.Fatal(err)
	}
}

func TestTraefikCoreAssetRequiresSecurityHeadersAndNoProxyBuffering(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repoRootForTraefikTest(t), "traefik", "dynamic", "core.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderTraefikCoreAsset(contents, Plan{PublicURL: "https://console.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTraefikCoreAsset(rendered); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Path(`/healthz`)", "Path(`/readyz`)", "Path(`/version`)", "Strict-Transport-Security", "Access-Control-Allow-Origin: \"*\""} {
		if strings.Contains(string(rendered), forbidden) {
			t.Fatalf("core asset contains forbidden marker %q", forbidden)
		}
	}
	unsafe := strings.Replace(string(rendered), "PathPrefix(`/v1/`)", "(PathPrefix(`/v1/`) || PathPrefix(`/healthz`))", 1)
	if err := validateTraefikCoreAsset([]byte(unsafe)); err == nil {
		t.Fatal("core API route with an internal health prefix was accepted")
	}
}

func TestTraefikCoreAssetKeepsStreamingRoutesOutOfRequestBuffering(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repoRootForTraefikTest(t), "traefik", "dynamic", "core.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderTraefikCoreAsset(contents, Plan{PublicURL: "https://console.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"stealth-admin-realtime:",
		"Path(`/v1/admin/realtime`)",
		"stealth-project-realtime:",
		"PathRegexp(`^/v1/projects/[0-9a-fA-F-]{36}/realtime$`)",
	} {
		if !strings.Contains(string(rendered), marker) {
			t.Fatalf("core asset is missing unbuffered streaming route marker %q", marker)
		}
	}
	if err := validateTraefikCoreAsset(rendered); err != nil {
		t.Fatalf("streaming route core validation failed: %v", err)
	}
}

func TestTraefikManagedAssetsInstallAndRepair(t *testing.T) {
	assetServer := newEngineAssetServer(t, "v1.2.3")
	t.Run("fresh installation", func(t *testing.T) {
		layout, err := NewLayout(filepath.Join(t.TempDir(), "stealth"))
		if err != nil {
			t.Fatal(err)
		}
		config, err := GenerateConfig(ConfigOptions{
			Version:           "v1.2.3",
			PublicURL:         "https://console.example.test",
			GitHubAppClientID: "Iv1.test-client-id",
			DockerGID:         42,
		})
		if err != nil {
			t.Fatal(err)
		}
		engine := New(Options{AssetBaseURL: assetServer.URL, Runner: &fakeRunner{}})
		if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: "v1.2.3", PublicURL: "https://console.example.test", ConfigContents: config}); err != nil {
			t.Fatal(err)
		}
		assertInstalledTraefikAssets(t, layout, "console.example.test")
	})

	t.Run("repair restores release files", func(t *testing.T) {
		layout := writeEngineFixture(t, false)
		if err := os.Remove(layout.TraefikStatic); err != nil {
			t.Fatal(err)
		}
		engine := New(Options{AssetBaseURL: assetServer.URL, Runner: &fakeRunner{}})
		if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: "v1.2.3", Existing: true}); err != nil {
			t.Fatal(err)
		}
		assertInstalledTraefikAssets(t, layout, "127.0.0.1")
	})
}

func assertInstalledTraefikAssets(t *testing.T, layout Layout, host string) {
	t.Helper()
	static, err := os.ReadFile(layout.TraefikStatic)
	if err != nil {
		t.Fatalf("read installed static config: %v", err)
	}
	if err := validateTraefikStaticAsset(static); err != nil {
		t.Fatal(err)
	}
	core, err := os.ReadFile(layout.TraefikCore)
	if err != nil {
		t.Fatalf("read installed core config: %v", err)
	}
	if strings.Contains(string(core), traefikPublicHostPlaceholder) || !strings.Contains(string(core), "Host(`"+host+"`)") {
		t.Fatalf("installed core config = %q", core)
	}
	if _, err := os.Stat(filepath.Join(layout.TraefikGenerated, ".gitkeep")); err != nil {
		t.Fatalf("generated route directory asset is missing: %v", err)
	}
	if _, err := os.Stat(layout.TraefikReloadMarker); err != nil {
		t.Fatalf("file-provider reload marker is missing: %v", err)
	}
	for _, path := range []string{layout.TraefikDir, layout.TraefikDynamic, layout.TraefikGenerated} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o755 {
			t.Fatalf("Traefik directory %s mode = %o", path, info.Mode().Perm())
		}
	}
}

func TestEnsureTraefikDirectoriesRepairsNarrowWorkerOwnership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "stealth")
	layout, err := NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.TraefikDynamic, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.TraefikDynamic, ".reload.yaml"), []byte("legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.TraefikStatic, []byte("static\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.TraefikCore, []byte("core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.TraefikGenerated, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("generated state must survive repair\n")
	if err := os.WriteFile(filepath.Join(layout.TraefikGenerated, "platform-sites.yaml"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	type ownershipCall struct {
		path string
		uid  int
		gid  int
	}
	var calls []ownershipCall
	setter := func(path string, uid, gid int) error {
		calls = append(calls, ownershipCall{path: path, uid: uid, gid: gid})
		return nil
	}
	if err := ensureTraefikDirectories(layout, setter); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(layout.TraefikReloadMarker)
	if err != nil {
		t.Fatalf("top-level reload marker missing: %v", err)
	}
	if string(marker) != "legacy\n" {
		t.Fatalf("existing reload marker was changed: %q", marker)
	}
	got, err := os.ReadFile(filepath.Join(layout.TraefikGenerated, "platform-sites.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("generated state changed during repair: %q", got)
	}
	var dynamicCall, generatedCall, markerCall, staticCall, coreCall bool
	for _, call := range calls {
		if call.path == layout.TraefikDynamic && call.uid == StealthRuntimeUID && call.gid == StealthRuntimeGID {
			dynamicCall = true
		}
		if call.path == layout.TraefikGenerated && call.uid == StealthRuntimeUID && call.gid == StealthRuntimeGID {
			generatedCall = true
		}
		if call.path == layout.TraefikReloadMarker && call.uid == StealthRuntimeUID && call.gid == StealthRuntimeGID {
			markerCall = true
		}
		if call.path == layout.TraefikStatic && call.uid == os.Geteuid() && call.gid == os.Getegid() {
			staticCall = true
		}
		if call.path == layout.TraefikCore && call.uid == os.Geteuid() && call.gid == os.Getegid() {
			coreCall = true
		}
	}
	if !dynamicCall || !generatedCall || !markerCall || !staticCall || !coreCall {
		t.Fatalf("runtime ownership was not repaired: %+v", calls)
	}
	for _, path := range []string{layout.TraefikDir, layout.TraefikDynamic, layout.TraefikGenerated} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("%s mode = %o, want 755", path, info.Mode().Perm())
		}
	}
	markerInfo, err := os.Stat(layout.TraefikReloadMarker)
	if err != nil {
		t.Fatal(err)
	}
	if markerInfo.Mode().Perm() != 0o644 {
		t.Fatalf("reload marker mode = %o, want 644", markerInfo.Mode().Perm())
	}
}

func TestEnsureTraefikDirectoriesRejectsSymlinkAndNonDirectory(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "stealth")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "traefik")); err != nil {
			t.Skipf("symlink test unavailable: %v", err)
		}
		layout, err := NewLayout(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := ensureTraefikDirectories(layout, func(string, int, int) error { return nil }); err == nil {
			t.Fatal("Traefik directory symlink was accepted")
		}
	})

	t.Run("non-directory", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "stealth")
		layout, err := NewLayout(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(layout.TraefikDynamic, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(layout.TraefikGenerated, []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ensureTraefikDirectories(layout, func(string, int, int) error { return nil }); err == nil {
			t.Fatal("Traefik generated file was accepted as a directory")
		}
	})
}

func repoRootForTraefikTest(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}
