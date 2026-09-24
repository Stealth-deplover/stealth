package installengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

const buildKitAppArmorProfile = `# Managed by Stealth. Changes will be replaced by the installer.
abi <abi/4.0>,
include <tunables/global>

profile stealth-buildkit-rootless flags=(unconfined) {
  # Ubuntu 24.04+ requires this permission for rootlesskit user namespaces.
  userns,
}
`

func validateBuildKitAppArmorProfileAsset(contents []byte) error {
	if !bytes.Equal(contents, []byte(buildKitAppArmorProfile)) {
		return errors.New("BuildKit AppArmor profile must contain only Stealth's rootless user namespace policy")
	}
	return nil
}

func (e *Engine) ensureBuildKitAppArmorProfile(ctx context.Context, plan Plan) error {
	values, err := ReadEnvFile(plan.Layout.EnvFile)
	if err != nil {
		return fmt.Errorf("read BuildKit AppArmor configuration: %w", err)
	}
	profile := values["APPS_BUILDKIT_APPARMOR_PROFILE"]
	if !validBuildKitAppArmorProfile(profile) {
		return errorsf("APPS_BUILDKIT_APPARMOR_PROFILE must be unconfined or " + BuildKitAppArmorProfileName)
	}
	if profile == "unconfined" {
		return nil
	}

	assetPath := filepath.Join(plan.Layout.Root, "buildkit", "stealth-buildkit-rootless.apparmor")
	contents, err := os.ReadFile(assetPath)
	if err != nil {
		return fmt.Errorf("read managed BuildKit AppArmor profile: %w", err)
	}
	if err := validateBuildKitAppArmorProfileAsset(contents); err != nil {
		return err
	}
	if err := installManagedAppArmorProfile(e.appArmorProfilePath, contents); err != nil {
		if os.Geteuid() != 0 && errors.Is(err, os.ErrPermission) {
			return installAppArmorProfileWithSudo(ctx, e.runner, e.output, contents, e.appArmorProfilePath)
		} else {
			return err
		}
	}
	name, args := "apparmor_parser", []string{"-r", "-W", e.appArmorProfilePath}
	if os.Geteuid() != 0 {
		name = "sudo"
		args = append([]string{"apparmor_parser"}, args...)
	}
	if err := e.runner.Run(ctx, filepath.Dir(e.appArmorProfilePath), e.output, e.output, name, args...); err != nil {
		return fmt.Errorf("load rootless BuildKit AppArmor user namespace profile: %w", err)
	}
	return nil
}

func installAppArmorProfileWithSudo(ctx context.Context, runner CommandRunner, output io.Writer, contents []byte, path string) error {
	inputRunner, ok := runner.(InputCommandRunner)
	if !ok {
		return errors.New("installing the BuildKit AppArmor profile requires a command runner with trusted input support")
	}
	stagingPath := buildKitAppArmorStagingPath(path)
	if err := inputRunner.RunInput(ctx, filepath.Dir(path), bytes.NewReader(contents), io.Discard, output, "sudo", "tee", stagingPath); err != nil {
		return fmt.Errorf("stage rootless BuildKit AppArmor profile (requires root access): %w", err)
	}
	cleanupStaging := func() {
		_ = runner.Run(ctx, filepath.Dir(path), io.Discard, io.Discard, "sudo", "rm", "-f", "--", stagingPath)
	}
	if err := runner.Run(ctx, filepath.Dir(path), output, output, "sudo", "chmod", "0644", stagingPath); err != nil {
		cleanupStaging()
		return fmt.Errorf("set staged BuildKit AppArmor profile permissions: %w", err)
	}
	if err := runner.Run(ctx, filepath.Dir(path), output, output, "sudo", "mv", "--", stagingPath, path); err != nil {
		cleanupStaging()
		return fmt.Errorf("publish BuildKit AppArmor profile: %w", err)
	}
	if err := runner.Run(ctx, filepath.Dir(path), output, output, "sudo", "apparmor_parser", "-r", "-W", path); err != nil {
		return fmt.Errorf("load rootless BuildKit AppArmor user namespace profile: %w", err)
	}
	return nil
}

func buildKitAppArmorStagingPath(path string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(path)), "."+filepath.Base(path)+".install-"+strconv.Itoa(os.Getpid()))
}

func installManagedAppArmorProfile(path string, contents []byte) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != BuildKitAppArmorProfileName {
		return errors.New("BuildKit AppArmor profile path is invalid")
	}
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect AppArmor profile directory: %w", err)
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("AppArmor profile directory must be a real directory")
	}
	if err := validateBuildKitAppArmorProfileAsset(contents); err != nil {
		return err
	}

	current, err := os.ReadFile(path)
	if err == nil {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("inspect existing AppArmor profile: %w", statErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("existing BuildKit AppArmor profile is not a regular file")
		}
		if bytes.Equal(current, contents) {
			return nil
		}
		if !bytes.HasPrefix(current, []byte(appArmorProfileManagedMark+"\n")) {
			return errors.New("an unmanaged AppArmor profile already uses the Stealth BuildKit profile path")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read existing AppArmor profile: %w", err)
	}
	if err := WriteAtomic(path, contents, 0o644); err != nil {
		return fmt.Errorf("install managed BuildKit AppArmor profile: %w", err)
	}
	return nil
}

// RemoveManagedBuildKitAppArmorProfile unloads and removes only the policy
// file owned by this release. An unrelated file at the fixed path is kept and
// reported as a conflict.
func RemoveManagedBuildKitAppArmorProfile(ctx context.Context, runner CommandRunner, stdout, stderr io.Writer) error {
	return removeManagedBuildKitAppArmorProfileAt(ctx, runner, stdout, stderr, BuildKitAppArmorProfilePath)
}

func removeManagedBuildKitAppArmorProfileAt(ctx context.Context, runner CommandRunner, stdout, stderr io.Writer, path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect managed BuildKit AppArmor profile: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to remove an unsafe BuildKit AppArmor profile path")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read managed BuildKit AppArmor profile: %w", err)
	}
	if !bytes.HasPrefix(contents, []byte(appArmorProfileManagedMark+"\n")) {
		return errors.New("refusing to remove an AppArmor profile that is not managed by Stealth")
	}
	if runner == nil {
		return errors.New("AppArmor command runner is required to remove the BuildKit profile")
	}
	name, args := "apparmor_parser", []string{"-R", path}
	if os.Geteuid() != 0 {
		name = "sudo"
		args = append([]string{"apparmor_parser"}, args...)
	}
	if err := runner.Run(ctx, filepath.Dir(path), stdout, stderr, name, args...); err != nil {
		return fmt.Errorf("unload managed BuildKit AppArmor profile: %w", err)
	}
	if err := removeAndSync(path); err != nil {
		if os.Geteuid() != 0 && errors.Is(err, os.ErrPermission) {
			if sudoErr := runner.Run(ctx, filepath.Dir(path), stdout, stderr, "sudo", "rm", "--", path); sudoErr != nil {
				return fmt.Errorf("remove managed BuildKit AppArmor profile (requires root access): %w", sudoErr)
			}
			return nil
		}
		return fmt.Errorf("remove managed BuildKit AppArmor profile: %w", err)
	}
	return nil
}
