package cli

// Uninstall workflow owns option parsing, safety planning, Docker/filesystem
// effects, and deterministic non-interactive output. Interactive state and
// rendering live in uninstall_tui.go.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type uninstallMode int

const (
	uninstallServices uninstallMode = iota
	uninstallConfiguration
	uninstallPurge
)

type uninstallOptions struct {
	mode    uninstallMode
	modeSet bool
	yes     bool
	dryRun  bool
}

type uninstallVolume struct {
	label       string
	name        string
	composeName string
}

type uninstallPlan struct {
	layout          InstallLayout
	mode            uninstallMode
	config          map[string]string
	configPresent   bool
	configErr       error
	composePresent  bool
	proxyPresent    bool
	versionPresent  bool
	statePresent    bool
	partial         bool
	unsafeReason    string
	unknownEntries  []string
	volumes         []uninstallVolume
	externalStorage bool
}

func (a *App) runUninstall(args []string) int {
	initTerminalStyles()
	options, parseErr := parseUninstallOptions(args, a.errOut)
	if parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	if !installationExists(layout) {
		fmt.Fprintln(a.out, "No Stealth installation was found.")
		return 0
	}

	plan := buildUninstallPlan(layout, options.mode)
	if plan.unsafeReason != "" {
		fmt.Fprintf(a.errOut, "cannot safely inspect the Stealth installation: %s\n", plan.unsafeReason)
		return 1
	}

	interactive := a.hasInteractiveTerminal()
	if !options.modeSet {
		if interactive {
			// The menu supplies the mode. The dry-run flag is carried through the
			// model after the operator makes a selection.
		} else {
			fmt.Fprintln(a.errOut, "Non-interactive uninstall requires an explicit mode.")
			fmt.Fprintln(a.errOut, "Use `stealth uninstall --keep-data --yes` or `stealth uninstall --purge --yes`.")
			return 2
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if interactive {
		if options.dryRun && options.modeSet && options.yes {
			printUninstallPlan(a.out, plan)
			fmt.Fprintln(a.out, "\nDry run complete. No changes were made.")
			return 0
		}
		return a.runUninstallTUI(ctx, plan, options)
	}
	return a.runUninstallPlain(ctx, plan, options)
}

func parseUninstallOptions(args []string, errOut io.Writer) (uninstallOptions, error) {
	fs := flag.NewFlagSet("stealth uninstall", flag.ContinueOnError)
	fs.SetOutput(errOut)
	keepData := fs.Bool("keep-data", false, "remove services and preserve persistent data")
	purge := fs.Bool("purge", false, "permanently remove services, data, configuration, and secrets")
	yes := fs.Bool("yes", false, "skip confirmation; never selects purge by itself")
	dryRun := fs.Bool("dry-run", false, "show the removal plan without making changes")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: stealth uninstall [--keep-data|--purge] [--yes] [--dry-run]")
		fmt.Fprintln(errOut)
		fmt.Fprintln(errOut, "Interactive mode presents guided choices when run in a terminal.")
		fmt.Fprintln(errOut, "  --keep-data  remove services only; preserve all persistent data and config")
		fmt.Fprintln(errOut, "  --purge      permanently remove the instance and its project-owned data")
		fmt.Fprintln(errOut, "  --yes        skip confirmation; without a mode it means --keep-data")
		fmt.Fprintln(errOut, "  --dry-run    show what would be removed and preserved")
		fmt.Fprintln(errOut)
		fmt.Fprintln(errOut, "Examples:")
		fmt.Fprintln(errOut, "  stealth uninstall")
		fmt.Fprintln(errOut, "  stealth uninstall --keep-data --yes")
		fmt.Fprintln(errOut, "  stealth uninstall --purge --dry-run")
		fmt.Fprintln(errOut, "  stealth uninstall --purge --yes")
	}
	if err := fs.Parse(args); err != nil {
		return uninstallOptions{}, err
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "uninstall does not accept positional arguments")
		return uninstallOptions{}, errors.New("positional arguments")
	}
	if *keepData && *purge {
		fmt.Fprintln(errOut, "uninstall modes --keep-data and --purge are mutually exclusive")
		return uninstallOptions{}, errors.New("conflicting modes")
	}
	options := uninstallOptions{yes: *yes, dryRun: *dryRun}
	switch {
	case *purge:
		options.mode = uninstallPurge
		options.modeSet = true
	case *keepData:
		options.mode = uninstallServices
		options.modeSet = true
	case *yes:
		// --yes must remain a safe default. It is intentionally equivalent to
		// --keep-data when no explicit mode is supplied.
		options.mode = uninstallServices
		options.modeSet = true
	}
	return options, nil
}

func buildUninstallPlan(layout InstallLayout, mode uninstallMode) uninstallPlan {
	plan := uninstallPlan{
		layout:         layout,
		mode:           mode,
		config:         make(map[string]string),
		composePresent: safeRegularFile(layout.ComposeFile),
		proxyPresent:   safeRegularFile(layout.ProxyFile),
		versionPresent: safeRegularFile(layout.VersionFile),
		statePresent:   safeDirectory(layout.StateDir),
	}

	if pathPresent(layout.Root) && !safeDirectory(layout.Root) {
		plan.unsafeReason = fmt.Sprintf("installation root %s is not a normal directory", layout.Root)
		return plan
	}
	for _, path := range []string{
		layout.EnvFile,
		layout.ComposeFile,
		layout.ProxyFile,
		layout.VersionFile,
		layout.StateDir,
		filepath.Dir(layout.ProxyFile),
		filepath.Dir(filepath.Dir(layout.ProxyFile)),
	} {
		if pathPresent(path) && !safePathType(path) {
			plan.unsafeReason = fmt.Sprintf("refusing to operate on symlink or special path %s", path)
			return plan
		}
	}

	plan.configPresent = safeRegularFile(layout.EnvFile)
	if plan.configPresent {
		plan.config, plan.configErr = readEnvFile(layout.EnvFile)
	}
	if plan.configErr == nil {
		storageDriver := "local"
		if driver := strings.TrimSpace(plan.config["STORAGE_DRIVER"]); driver != "" {
			storageDriver = strings.ToLower(driver)
		}
		plan.externalStorage = storageDriver == "s3"
	}
	plan.volumes = configuredUninstallVolumes(plan.config)
	plan.unknownEntries = unknownLayoutEntries(layout)
	plan.partial = !plan.configPresent || plan.configErr != nil || !plan.composePresent || !plan.proxyPresent || !plan.versionPresent
	return plan
}

func configuredUninstallVolumes(values map[string]string) []uninstallVolume {
	postgres := configuredVolumeName(values, "POSTGRES_VOLUME_NAME", "stealth_postgres_data")
	storage := configuredVolumeName(values, "STORAGE_VOLUME_NAME", "stealth_storage")
	staging := configuredVolumeName(values, "FUNCTIONS_RUNNER_STAGING_VOLUME", "stealth_function_runner_staging")
	volumes := []uninstallVolume{
		{label: "PostgreSQL data volume", name: postgres, composeName: "postgres_data"},
	}
	if strings.EqualFold(strings.TrimSpace(values["STORAGE_DRIVER"]), "s3") {
		volumes = append(volumes, uninstallVolume{label: "Local staging/cache volume", name: storage, composeName: "stealth_storage"})
	} else {
		volumes = append(volumes, uninstallVolume{label: "Object storage and function/site artifacts", name: storage, composeName: "stealth_storage"})
	}
	volumes = append(volumes, uninstallVolume{label: "Function runner staging volume", name: staging, composeName: "function_runner_staging"})
	return uniqueUninstallVolumes(volumes)
}

func configuredVolumeName(values map[string]string, key, fallback string) string {
	value := strings.TrimSpace(values[key])
	if value == "" {
		return fallback
	}
	return value
}

func uniqueUninstallVolumes(volumes []uninstallVolume) []uninstallVolume {
	seen := make(map[string]struct{}, len(volumes))
	result := make([]uninstallVolume, 0, len(volumes))
	for _, volume := range volumes {
		if _, ok := seen[volume.name]; ok {
			continue
		}
		seen[volume.name] = struct{}{}
		result = append(result, volume)
	}
	return result
}

func pathPresent(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func safeRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func safeDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func safePathType(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return info.Mode().IsRegular() || info.IsDir()
}

func unknownLayoutEntries(layout InstallLayout) []string {
	if !safeDirectory(layout.Root) {
		return nil
	}
	allowed := map[string]bool{
		filepath.Base(layout.EnvFile):                               true,
		filepath.Base(layout.ComposeFile):                           true,
		filepath.Base(layout.VersionFile):                           true,
		filepath.Base(layout.StateDir):                              true,
		filepath.Base(filepath.Dir(filepath.Dir(layout.ProxyFile))): true,
	}
	var unknown []string
	entries, err := os.ReadDir(layout.Root)
	if err != nil {
		return []string{layout.Root + " (cannot inspect: " + err.Error() + ")"}
	}
	for _, entry := range entries {
		name := entry.Name()
		if !allowed[name] {
			unknown = append(unknown, filepath.Join(layout.Root, name))
			continue
		}
		if name != filepath.Base(filepath.Dir(filepath.Dir(layout.ProxyFile))) {
			continue
		}
		consoleDir := filepath.Join(layout.Root, name)
		if !entry.IsDir() {
			unknown = append(unknown, consoleDir)
			continue
		}
		deployDir := filepath.Dir(layout.ProxyFile)
		consoleEntries, consoleErr := os.ReadDir(consoleDir)
		if consoleErr != nil {
			unknown = append(unknown, consoleDir+" (cannot inspect: "+consoleErr.Error()+")")
			continue
		}
		for _, consoleEntry := range consoleEntries {
			if consoleEntry.Name() != filepath.Base(deployDir) {
				unknown = append(unknown, filepath.Join(consoleDir, consoleEntry.Name()))
			}
		}
		deployEntries, deployErr := os.ReadDir(deployDir)
		if deployErr != nil && !errors.Is(deployErr, os.ErrNotExist) {
			unknown = append(unknown, deployDir+" (cannot inspect: "+deployErr.Error()+")")
			continue
		}
		for _, deployEntry := range deployEntries {
			if deployEntry.Name() != filepath.Base(layout.ProxyFile) {
				unknown = append(unknown, filepath.Join(deployDir, deployEntry.Name()))
			}
		}
	}
	sort.Strings(unknown)
	return unknown
}

func uninstallModeName(mode uninstallMode) string {
	switch mode {
	case uninstallServices:
		return "Preserve data (services only)"
	case uninstallConfiguration:
		return "Remove services + local runtime files"
	case uninstallPurge:
		return "PURGE — permanently delete instance data"
	default:
		return "Unknown"
	}
}

func (p uninstallPlan) removalItems() []string {
	items := make([]string, 0, 16)
	if p.composePresent {
		for _, service := range []string{"API container", "Worker container", "Console container", "Proxy container", "PostgreSQL container", "Redis container", "Migration container"} {
			items = append(items, service)
		}
		items = append(items, "Compose network and runtime state")
	} else {
		items = append(items, "Compose services (already absent; Compose file is missing)")
	}
	if p.mode == uninstallConfiguration || p.mode == uninstallPurge {
		for _, asset := range p.localAssets(p.mode == uninstallPurge) {
			if asset.present {
				items = append(items, asset.label)
			}
		}
	}
	if p.mode == uninstallPurge {
		for _, volume := range p.volumes {
			items = append(items, volume.label+" ("+volume.name+")")
		}
	}
	return items
}

func (p uninstallPlan) preservedItems() []string {
	items := make([]string, 0, 16)
	if p.mode != uninstallPurge {
		for _, volume := range p.volumes {
			items = append(items, volume.label+" ("+volume.name+")")
		}
	}
	if p.mode == uninstallServices {
		for _, asset := range p.localAssets(false) {
			if asset.present {
				items = append(items, asset.label)
			}
		}
		if p.configPresent {
			items = append(items, "config.env (secrets and recovery configuration)")
		}
	}
	if p.mode == uninstallConfiguration && p.configPresent {
		items = append(items, "config.env (recovery secrets and encryption key)")
	}
	items = append(items, "Stealth CLI executable (not modified)")
	if p.externalStorage {
		items = append(items, "External S3 object storage (not managed by this CLI)")
	}
	return items
}

type uninstallAsset struct {
	label   string
	path    string
	present bool
}

func (p uninstallPlan) localAssets(includeConfig bool) []uninstallAsset {
	assets := []uninstallAsset{
		{label: "compose.production.yaml", path: p.layout.ComposeFile, present: p.composePresent},
		{label: "console/deploy/nginx.conf", path: p.layout.ProxyFile, present: p.proxyPresent},
		{label: "VERSION", path: p.layout.VersionFile, present: p.versionPresent},
		{label: "state/", path: p.layout.StateDir, present: p.statePresent},
	}
	if includeConfig {
		assets = append(assets, uninstallAsset{label: "config.env and local secrets", path: p.layout.EnvFile, present: p.configPresent})
	}
	return assets
}

func (p uninstallPlan) warnings() []string {
	var warnings []string
	if p.partial {
		var missing []string
		if !p.configPresent {
			missing = append(missing, "config.env")
		} else if p.configErr != nil {
			missing = append(missing, "readable config.env")
		}
		if !p.composePresent {
			missing = append(missing, "compose.production.yaml")
		}
		if !p.proxyPresent {
			missing = append(missing, "console/deploy/nginx.conf")
		}
		if !p.versionPresent {
			missing = append(missing, "VERSION")
		}
		if len(missing) > 0 {
			warnings = append(warnings, "Partial installation detected; missing "+strings.Join(missing, ", ")+".")
		}
	}
	if p.configErr != nil {
		warnings = append(warnings, "config.env is preserved but could not be parsed: "+p.configErr.Error())
	}
	if p.mode == uninstallConfiguration && p.configPresent {
		warnings = append(warnings, "config.env is kept because FUNCTIONS_SECRET_KEY and database credentials are required to recover preserved data.")
	}
	if p.externalStorage {
		warnings = append(warnings, "External S3 object storage is not deleted; remove its owned bucket/prefix with provider tooling after verifying ownership.")
	}
	if len(p.unknownEntries) > 0 {
		warnings = append(warnings, "Unrecognized files are preserved: "+strings.Join(p.unknownEntries, ", "))
	}
	if p.mode == uninstallPurge && !p.canPurge() {
		warnings = append(warnings, "Purge is blocked until the complete, readable installation layout can be validated; no data will be deleted.")
	}
	return warnings
}

func (p uninstallPlan) canPurge() bool {
	return p.configPresent && p.configErr == nil && p.composePresent && len(p.unknownEntries) == 0 && p.unsafeReason == ""
}

func printUninstallPlan(w io.Writer, plan uninstallPlan) {
	fmt.Fprintln(w, "Removal plan")
	fmt.Fprintf(w, "Instance       %s\n", plan.layout.Root)
	fmt.Fprintf(w, "Mode           %s\n", uninstallModeName(plan.mode))
	if plan.partial {
		fmt.Fprintln(w, "Status         Partial installation")
	}
	fmt.Fprintln(w, "\nWill remove")
	for _, item := range plan.removalItems() {
		fmt.Fprintf(w, "✓ %s\n", item)
	}
	fmt.Fprintln(w, "\nWill preserve")
	for _, item := range plan.preservedItems() {
		fmt.Fprintf(w, "✓ %s\n", item)
	}
	if warnings := plan.warnings(); len(warnings) > 0 {
		fmt.Fprintln(w, "\nWarnings")
		for _, warning := range warnings {
			fmt.Fprintf(w, "! %s\n", warning)
		}
	}
	if plan.mode == uninstallPurge {
		fmt.Fprintln(w, "\nThis is permanent. Back up important database and storage data before continuing.")
	}
}

type uninstallOperation struct {
	name   string
	action func(context.Context) error
}

func (a *App) uninstallOperations(plan uninstallPlan) []uninstallOperation {
	operations := make([]uninstallOperation, 0, 6)
	if plan.mode == uninstallPurge {
		operations = append(operations, uninstallOperation{
			name:   "Validate project-owned Docker resources",
			action: func(ctx context.Context) error { return a.validatePurgeScope(ctx, plan) },
		})
	}
	if plan.composePresent {
		operations = append(operations, uninstallOperation{
			name:   "Remove Stealth services and network",
			action: func(ctx context.Context) error { return a.removeComposeResources(ctx, plan) },
		})
	} else {
		operations = append(operations, uninstallOperation{
			name:   "Confirm Compose runtime is already absent",
			action: func(context.Context) error { return nil },
		})
	}
	if plan.mode == uninstallPurge {
		operations = append(operations, uninstallOperation{
			name:   "Verify services and persistent data removal",
			action: func(ctx context.Context) error { return a.verifyDockerPurge(ctx, plan) },
		})
	} else {
		operations = append(operations, uninstallOperation{
			name:   "Verify service removal",
			action: func(ctx context.Context) error { return a.verifyServicesRemoved(ctx, plan) },
		})
	}
	if plan.mode == uninstallConfiguration {
		operations = append(operations, uninstallOperation{
			name:   "Remove local runtime files",
			action: func(context.Context) error { return removeLocalRuntimeFiles(plan) },
		})
	}
	if plan.mode == uninstallPurge {
		operations = append(operations, uninstallOperation{
			name:   "Remove installation state and secrets",
			action: func(context.Context) error { return removeAllLocalFiles(plan) },
		})
	}
	operations = append(operations, uninstallOperation{
		name:   "Verify uninstall",
		action: func(context.Context) error { return verifyLocalUninstall(plan) },
	})
	return operations
}

func (a *App) removeComposeResources(ctx context.Context, plan uninstallPlan) error {
	if !plan.configPresent || plan.configErr != nil {
		return fmt.Errorf("cannot remove Docker services safely because config.env is missing or unreadable")
	}
	args := a.composeArgs(plan.layout, "down", "--remove-orphans")
	if plan.mode == uninstallPurge {
		args = a.composeArgs(plan.layout, "down", "--volumes", "--remove-orphans")
	}
	return a.runCommandCaptured(ctx, plan.layout.Root, "docker", args...)
}

func (a *App) verifyServicesRemoved(ctx context.Context, plan uninstallPlan) error {
	if !plan.composePresent {
		return nil
	}
	if !plan.configPresent || plan.configErr != nil {
		return fmt.Errorf("cannot verify Docker services safely because config.env is missing or unreadable")
	}
	output, err := a.runner.Output(ctx, plan.layout.Root, "docker", a.composeArgs(plan.layout, "ps", "-aq")...)
	if err != nil {
		return fmt.Errorf("verify Docker services: %w", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		return fmt.Errorf("Docker Compose still reports running or stopped containers")
	}
	return nil
}

func (a *App) validatePurgeScope(ctx context.Context, plan uninstallPlan) error {
	if !plan.canPurge() {
		return fmt.Errorf("purge requires a complete installation with readable config.env, Compose, and no unrecognized local files")
	}
	seen := make(map[string]struct{}, len(plan.volumes))
	declared := make(map[string]struct{}, len(plan.volumes))
	for _, volume := range plan.volumes {
		if !validDockerResourceName(volume.name) {
			return fmt.Errorf("refusing to purge invalid configured volume name %q", volume.name)
		}
		if _, ok := seen[volume.name]; ok {
			return fmt.Errorf("configured volume names are not unique")
		}
		seen[volume.name] = struct{}{}
		declared[volume.composeName] = struct{}{}
	}
	contents, err := os.ReadFile(plan.layout.ComposeFile)
	if err != nil {
		return fmt.Errorf("read Compose file for purge validation: %w", err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(line, "#", 2)[0]), "external: true") {
			return fmt.Errorf("refusing to purge a Compose layout with external resources")
		}
	}
	output, err := a.runner.Output(ctx, plan.layout.Root, "docker", a.composeArgs(plan.layout, "config", "--volumes")...)
	if err != nil {
		return fmt.Errorf("validate Compose volumes: %w", err)
	}
	actual := make(map[string]struct{})
	for _, raw := range strings.Split(string(output), "\n") {
		name := strings.TrimSpace(raw)
		if name != "" {
			actual[name] = struct{}{}
		}
	}
	if len(actual) != len(declared) {
		return fmt.Errorf("Compose declares unexpected persistent resources; refusing purge")
	}
	for name := range declared {
		if _, ok := actual[name]; !ok {
			return fmt.Errorf("Compose volume %q was not confirmed as project-owned", name)
		}
	}
	if err := a.validateExistingVolumeOwnership(ctx, plan); err != nil {
		return err
	}
	return nil
}

func (a *App) validateExistingVolumeOwnership(ctx context.Context, plan uninstallPlan) error {
	output, err := a.runner.Output(ctx, "", "docker", "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return fmt.Errorf("inspect existing Docker volumes: %w", err)
	}
	existing := make(map[string]struct{})
	for _, raw := range strings.Split(string(output), "\n") {
		name := strings.TrimSpace(raw)
		if name != "" {
			existing[name] = struct{}{}
		}
	}
	projectName := strings.TrimSpace(plan.config["COMPOSE_PROJECT_NAME"])
	if projectName == "" {
		projectName = "stealth"
	}
	for _, volume := range plan.volumes {
		if _, ok := existing[volume.name]; !ok {
			continue
		}
		labels, inspectErr := a.runner.Output(ctx, "", "docker", "volume", "inspect", "--format", "{{ index .Labels \"com.docker.compose.project\" }}\t{{ index .Labels \"com.docker.compose.volume\" }}", volume.name)
		if inspectErr != nil {
			return fmt.Errorf("verify ownership of Docker volume %q: %w", volume.name, inspectErr)
		}
		fields := strings.SplitN(strings.TrimSpace(string(labels)), "\t", 2)
		if len(fields) != 2 || fields[0] != projectName || fields[1] != volume.composeName {
			return fmt.Errorf("Docker volume %q is not labeled as Compose project %q volume %q; refusing purge", volume.name, projectName, volume.composeName)
		}
	}
	return nil
}

func (a *App) verifyDockerPurge(ctx context.Context, plan uninstallPlan) error {
	if err := a.verifyServicesRemoved(ctx, plan); err != nil {
		return err
	}
	output, err := a.runner.Output(ctx, "", "docker", "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return fmt.Errorf("verify Docker volumes: %w", err)
	}
	remaining := make(map[string]struct{})
	for _, raw := range strings.Split(string(output), "\n") {
		name := strings.TrimSpace(raw)
		if name != "" {
			remaining[name] = struct{}{}
		}
	}
	for _, volume := range plan.volumes {
		if _, ok := remaining[volume.name]; ok {
			return fmt.Errorf("project-owned volume %q still exists", volume.name)
		}
	}
	return nil
}

func validDockerResourceName(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for index, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' && char != '-' && char != '.' {
			return false
		}
		if index == 0 && char == '.' {
			return false
		}
	}
	return true
}

func removeLocalRuntimeFiles(plan uninstallPlan) error {
	for _, asset := range plan.localAssets(false) {
		if !asset.present {
			continue
		}
		if err := removeSafePath(asset.path); err != nil {
			return fmt.Errorf("remove %s: %w", asset.label, err)
		}
	}
	return nil
}

func removeAllLocalFiles(plan uninstallPlan) error {
	if err := removeLocalRuntimeFiles(plan); err != nil {
		return err
	}
	if plan.configPresent {
		if err := removeSafePath(plan.layout.EnvFile); err != nil {
			return fmt.Errorf("remove config.env and local secrets: %w", err)
		}
	}
	for _, path := range []string{filepath.Dir(plan.layout.ProxyFile), filepath.Dir(filepath.Dir(plan.layout.ProxyFile))} {
		if err := removeEmptyDirectory(path); err != nil {
			return err
		}
	}
	return removeEmptyDirectory(plan.layout.Root)
}

func removeSafePath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
		return fmt.Errorf("refusing to remove unsafe path %s", path)
	}
	if info.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func removeEmptyDirectory(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTEMPTY) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove empty directory %s: %w", path, err)
	}
	return nil
}

func verifyLocalUninstall(plan uninstallPlan) error {
	switch plan.mode {
	case uninstallServices:
		return nil
	case uninstallConfiguration:
		for _, asset := range plan.localAssets(false) {
			if pathPresent(asset.path) {
				return fmt.Errorf("local runtime file %s remains", asset.label)
			}
		}
		if plan.configPresent && !safeRegularFile(plan.layout.EnvFile) {
			return fmt.Errorf("config.env was not preserved")
		}
		return nil
	case uninstallPurge:
		for _, asset := range plan.localAssets(true) {
			if pathPresent(asset.path) {
				return fmt.Errorf("local installation state %s remains", asset.label)
			}
		}
		if pathPresent(plan.layout.Root) {
			return fmt.Errorf("installation directory %s remains", plan.layout.Root)
		}
		return nil
	default:
		return fmt.Errorf("unknown uninstall mode")
	}
}

func (a *App) runUninstallPlain(ctx context.Context, plan uninstallPlan, options uninstallOptions) int {
	printUninstallPlan(a.out, plan)
	if options.dryRun {
		fmt.Fprintln(a.out, "\nDry run complete. No changes were made.")
		return 0
	}
	if !options.yes {
		fmt.Fprintln(a.errOut, "Refusing to uninstall without --yes because this output is not a TTY.")
		return 2
	}
	operations := a.uninstallOperations(plan)
	fmt.Fprintln(a.out, "\nRemoving Stealth")
	for index, operation := range operations {
		fmt.Fprintf(a.out, "[%d/%d] %s... ", index+1, len(operations), operation.name)
		err := operation.action(ctx)
		if err != nil {
			fmt.Fprintln(a.out, "failed")
			a.printUninstallFailure(plan, err)
			return 1
		}
		fmt.Fprintln(a.out, "done")
	}
	a.printUninstallSuccess(plan)
	return 0
}

func (a *App) printUninstallFailure(plan uninstallPlan, err error) {
	fmt.Fprintf(a.errOut, "\nUninstall incomplete: %v\n", err)
	if plan.mode == uninstallPurge {
		fmt.Fprintln(a.errOut, "No further destructive cleanup was attempted. Inspect the instance before retrying.")
	} else {
		fmt.Fprintln(a.errOut, "Persistent data was not targeted by this mode.")
	}
	fmt.Fprintln(a.errOut, "Try `stealth doctor` for diagnostics.")
	if plan.composePresent {
		fmt.Fprintln(a.errOut, "Use `stealth logs <service>` if Docker services need inspection.")
	}
}

func (a *App) printUninstallSuccess(plan uninstallPlan) {
	switch plan.mode {
	case uninstallServices:
		fmt.Fprintln(a.out, "\nStealth services were removed. Persistent data, config.env, and recovery files were preserved.")
	case uninstallConfiguration:
		fmt.Fprintln(a.out, "\nStealth services and local runtime files were removed.")
		fmt.Fprintln(a.out, "Persistent data was preserved. config.env remains because it contains recovery secrets and the encryption key.")
	case uninstallPurge:
		fmt.Fprintln(a.out, "\nStealth instance data, services, configuration, and project-owned Docker volumes were permanently removed.")
		if plan.externalStorage {
			fmt.Fprintln(a.out, "External S3 object storage was preserved; remove it separately after verifying ownership.")
		}
	}
	if binary := detectedCLIBinaryPath(); binary != "" {
		fmt.Fprintf(a.out, "The Stealth CLI is still installed at %s.\n", binary)
		fmt.Fprintf(a.out, "To remove it manually: rm %s\n", binary)
	}
}

func detectedCLIBinaryPath() string {
	path, err := os.Executable()
	if err != nil || filepath.Base(path) != "stealth" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = resolved
	}
	return path
}
