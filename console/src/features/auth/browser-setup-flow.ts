"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { apiUrl } from "@/api/client";
import {
  useSaveSetupCloudflareToken,
  useSaveSetupConfig,
  useSaveSetupGitHubManual,
  useStartSetupGitHubAuthorization,
  useStartSetupGitHubManifest,
  useStartSetupInstall,
  useTestSetupDatabase,
  useTestSetupRedis,
  useTestSetupStorage,
  useVerifyBootstrapCode,
  useIssueSetupHandoffToken,
  useCreateSetupCloudflareTunnel,
} from "@/api/mutations";
import {
  useSetupCloudflareAccounts,
  useSetupCloudflareZones,
  useSetupPreflight,
  useSetupStatus,
} from "@/api/queries";
import {
  configFromState,
  configSchema,
  defaultConfig,
  manualGitHubSchema,
  setupCodeSchema,
  toSetupRequest,
  type ConfigValues,
  type ManualGitHubValues,
  type SetupState,
  type SetupStep,
} from "./browser-setup-model";

export function useBrowserSetupFlow() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const setupStatus = useSetupStatus();
  const state = setupStatus.data?.state;
  const callbackStatus = [
    searchParams.get("github"),
    searchParams.get("cloudflare"),
  ]
    .filter(Boolean)
    .join(":");
  const configForm = useForm<ConfigValues>({
    resolver: zodResolver(configSchema),
    defaultValues: defaultConfig,
    mode: "onTouched",
  });
  const manualForm = useForm<ManualGitHubValues>({
    resolver: zodResolver(manualGitHubSchema),
    defaultValues: {
      client_id: "",
      client_secret: "",
      private_key: "",
      webhook_secret: "",
    },
    mode: "onTouched",
  });
  const [step, setStep] = useState<SetupStep>("welcome");
  const [setupCode, setSetupCode] = useState("");
  const [setupVerified, setSetupVerified] = useState(false);
  const [providerMode, setProviderMode] = useState<"manifest" | "manual">(
    "manifest",
  );
  const [cloudflareToken, setCloudflareToken] = useState("");
  const [cloudflareAccountID, setCloudflareAccountID] = useState<string | null>(
    null,
  );
  const [cloudflareZoneID, setCloudflareZoneID] = useState<string | null>(null);
  const [handoffToken, setHandoffToken] = useState("");
  const [installState, setInstallState] = useState<SetupState>();
  const [sseConnected, setSSEConnected] = useState(false);
  const [actionError, setActionError] = useState<unknown>();
  const [notice, setNotice] = useState("");
  const hydrated = useRef(false);
  const callbackHandled = useRef("");
  const handoffSubmitted = useRef(false);
  const verifyCode = useVerifyBootstrapCode();
  const saveConfig = useSaveSetupConfig();
  const startManifest = useStartSetupGitHubManifest();
  const startAuthorization = useStartSetupGitHubAuthorization();
  const saveManual = useSaveSetupGitHubManual();
  const saveCloudflareToken = useSaveSetupCloudflareToken();
  const createCloudflareTunnel = useCreateSetupCloudflareTunnel();
  const testDatabase = useTestSetupDatabase();
  const testRedis = useTestSetupRedis();
  const testStorage = useTestSetupStorage();
  const startInstall = useStartSetupInstall();
  const issueHandoff = useIssueSetupHandoffToken();
  const watchedConfig = useWatch({ control: configForm.control });
  const networkMode = watchedConfig.network_mode ?? defaultConfig.network_mode;
  const databaseMode =
    watchedConfig.database_mode ?? defaultConfig.database_mode;
  const redisMode = watchedConfig.redis_mode ?? defaultConfig.redis_mode;
  const storageMode = watchedConfig.storage_mode ?? defaultConfig.storage_mode;
  const requestedAccount =
    cloudflareAccountID ?? state?.draft.cloudflare_account_id ?? "";
  const requestedZone =
    cloudflareZoneID ?? state?.draft.cloudflare_zone_id ?? "";
  const ownerConfirmed = Boolean(
    setupStatus.data && !setupStatus.data.setup_required,
  );
  const setupAccess =
    setupVerified ||
    ownerConfirmed ||
    Boolean(state?.github.authorization_session);
  const cloudflareConnected = Boolean(
    state?.cloudflare.connected &&
    state.cloudflare.mode === "api_token" &&
    state.cloudflare.token_valid,
  );
  const preflight = useSetupPreflight(setupAccess);
  const accounts = useSetupCloudflareAccounts(
    setupAccess && cloudflareConnected,
  );
  const availableAccounts = accounts.data?.accounts;
  const selectedAccount =
    cloudflareConnected && availableAccounts?.length
      ? availableAccounts.some((account) => account.id === requestedAccount)
        ? requestedAccount
        : availableAccounts[0].id
      : requestedAccount;
  const zones = useSetupCloudflareZones(
    selectedAccount,
    setupAccess && cloudflareConnected && Boolean(availableAccounts?.length),
  );
  const availableZones = zones.data?.zones;
  const selectedZone = availableZones?.length
    ? availableZones.some((zone) => zone.id === requestedZone)
      ? requestedZone
      : availableZones[0].id
    : requestedZone;
  const activeStep =
    state?.phase === "installing" ||
    state?.phase === "failed" ||
    state?.phase === "handoff"
      ? "install"
      : step;

  useEffect(() => {
    if (!state || hydrated.current) return;
    configForm.reset(configFromState(state));
    setCloudflareAccountID(state.draft.cloudflare_account_id ?? "");
    setCloudflareZoneID(state.draft.cloudflare_zone_id ?? "");
    setSetupVerified(Boolean(state.github.authorization_session));
    hydrated.current = true;
  }, [configForm, state]);

  useEffect(() => {
    const callback = callbackStatus;
    if (!callback || callbackHandled.current === callback) return;
    callbackHandled.current = callback;
    void setupStatus.refetch();
    router.replace("/setup", { scroll: false });
  }, [callbackStatus, router, setupStatus]);

  useEffect(() => {
    if (activeStep !== "install") return;
    const source = new EventSource(apiUrl("/v1/setup/install/events"), {
      withCredentials: true,
    });
    source.onopen = () => setSSEConnected(true);
    source.addEventListener("snapshot", (event) => {
      try {
        setInstallState(JSON.parse(event.data) as SetupState);
      } catch {
        setActionError(
          new Error("Install progress returned an invalid snapshot."),
        );
      }
    });
    source.addEventListener("progress", (event) => {
      try {
        const progress = JSON.parse(event.data) as {
          step?: string;
          error?: string;
        };
        setInstallState((current) =>
          current
            ? {
                ...current,
                step: progress.step,
                error_message: progress.error,
              }
            : current,
        );
      } catch {
        setActionError(
          new Error("Install progress returned an invalid event."),
        );
      }
    });
    source.onerror = () => setSSEConnected(false);
    return () => source.close();
  }, [activeStep]);

  useEffect(() => {
    if (activeStep !== "install") return;
    const timer = window.setInterval(() => void setupStatus.refetch(), 2_000);
    return () => window.clearInterval(timer);
  }, [activeStep, setupStatus]);

  useEffect(() => {
    if (
      activeStep !== "install" ||
      !state ||
      (state.phase !== "complete" && state.phase !== "handoff") ||
      handoffSubmitted.current
    ) {
      return;
    }
    const publicURL =
      state.draft.public_url ?? configForm.getValues("public_url");
    if (!publicURL) return;
    handoffSubmitted.current = true;
    if (handoffToken) {
      return;
    }
    void issueHandoff
      .mutateAsync()
      .then((result) => {
        if (result?.token) {
          setHandoffToken(result.token);
        } else {
          handoffSubmitted.current = false;
          setActionError(
            new Error("The production session handoff was empty."),
          );
        }
      })
      .catch((error) => {
        handoffSubmitted.current = false;
        setActionError(error);
      });
  }, [activeStep, configForm, handoffToken, issueHandoff, state]);

  const persistConfig = useCallback(
    async (values: ConfigValues) => {
      const saved = await saveConfig.mutateAsync(toSetupRequest(values));
      await setupStatus.refetch();
      return saved;
    },
    [saveConfig, setupStatus],
  );

  const moveTo = (next: SetupStep) => {
    setActionError(undefined);
    setNotice("");
    setStep(next);
  };

  const verifySetupCode = async () => {
    const parsed = setupCodeSchema.safeParse(setupCode);
    if (!parsed.success) {
      setActionError(
        new Error(
          parsed.error.issues[0]?.message ??
            "Enter the setup code shown by the CLI.",
        ),
      );
      return;
    }
    setActionError(undefined);
    try {
      const result = await verifyCode.mutateAsync({ setup_code: parsed.data });
      if (!result) return;
      setSetupCode(parsed.data);
      setSetupVerified(true);
      await preflight.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const authorizeGitHubOwner = async () => {
    setActionError(undefined);
    try {
      const result = await startAuthorization.mutateAsync();
      if (!result?.authorization_url) {
        throw new Error("GitHub authorization URL was not returned.");
      }
      window.location.assign(result.authorization_url);
    } catch (error) {
      setActionError(error);
    }
  };

  const saveManualGitHub = manualForm.handleSubmit(async (values) => {
    setActionError(undefined);
    try {
      await saveManual.mutateAsync(values);
      await setupStatus.refetch();
      setNotice("GitHub App credentials saved in encrypted setup state.");
    } catch (error) {
      setActionError(error);
    }
  });

  const connectCloudflareToken = async () => {
    if (!cloudflareToken.trim()) {
      setActionError(new Error("Enter a Cloudflare API token."));
      return;
    }
    setActionError(undefined);
    try {
      await saveCloudflareToken.mutateAsync({
        api_token: cloudflareToken.trim(),
      });
      setCloudflareToken("");
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    } finally {
      // React Query keeps mutation variables in memory. Clear the mutation
      // after the request so the provider token is not retained in the
      // browser mutation state after this one-shot setup action.
      saveCloudflareToken.reset();
    }
  };

  const continueCloudflareSetup = async () => {
    const values = configForm.getValues();
    if (
      networkMode === "cloudflare_tunnel" &&
      (!selectedAccount || !selectedZone || !values.hostname.trim())
    ) {
      setActionError(
        new Error("Choose an account, domain, and hostname first."),
      );
      return;
    }
    setActionError(undefined);
    try {
      await persistConfig(values);
      if (networkMode === "cloudflare_tunnel") {
        const result = await createCloudflareTunnel.mutateAsync({
          account_id: selectedAccount,
          zone_id: selectedZone,
          hostname: values.hostname.trim(),
        });
        if (result?.draft.public_url) {
          configForm.setValue("public_url", result.draft.public_url, {
            shouldDirty: false,
          });
        }
      }
      await setupStatus.refetch();
      moveTo("data");
    } catch (error) {
      setActionError(error);
    }
  };

  const testDatabaseConnection = async () => {
    setActionError(undefined);
    try {
      await persistConfig(configForm.getValues());
      await testDatabase.mutateAsync({
        url: configForm.getValues("database_url") || undefined,
      });
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const testRedisConnection = async () => {
    setActionError(undefined);
    try {
      await persistConfig(configForm.getValues());
      await testRedis.mutateAsync({
        url: configForm.getValues("redis_url") || undefined,
      });
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const testStorageConnection = async () => {
    setActionError(undefined);
    try {
      const values = configForm.getValues();
      await persistConfig(values);
      await testStorage.mutateAsync({
        endpoint: values.storage_s3_endpoint || undefined,
        region: values.storage_s3_region || undefined,
        bucket: values.storage_s3_bucket || undefined,
        access_key: values.storage_s3_access_key || undefined,
        secret_key: values.storage_s3_secret_key || undefined,
        use_ssl: values.storage_s3_use_ssl,
        path_style: values.storage_s3_path_style,
      });
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const startProductionInstall = async () => {
    setActionError(undefined);
    handoffSubmitted.current = false;
    try {
      if (!ownerConfirmed) {
        throw new Error(
          "Finish GitHub first-owner verification before installing.",
        );
      }
      const ticket = await issueHandoff.mutateAsync();
      if (!ticket?.token)
        throw new Error("The production session handoff is not ready.");
      setHandoffToken(ticket.token);
      await persistConfig(configForm.getValues());
      const result = await startInstall.mutateAsync();
      if (result) setInstallState(result.state);
      moveTo("install");
    } catch (error) {
      setActionError(error);
    }
  };

  const retryProductionInstall = async () => {
    setActionError(undefined);
    handoffSubmitted.current = false;
    try {
      const ticket = await issueHandoff.mutateAsync();
      if (ticket?.token) setHandoffToken(ticket.token);
      const result = await startInstall.mutateAsync();
      if (result) setInstallState(result.state);
    } catch (error) {
      setActionError(error);
    }
  };

  const checks = preflight.data?.checks ?? [];
  const checksPass =
    checks.length > 0 &&
    checks.every((check) => !check.required || check.status === "pass");
  const tunnelReady = Boolean(
    state?.draft.cloudflare_tunnel_id &&
    state.draft.cloudflare_record_id &&
    state.draft.hostname,
  );
  const dataReady =
    (databaseMode === "bundled" || Boolean(state?.draft.database_tested)) &&
    (redisMode === "bundled" || Boolean(state?.draft.redis_tested));
  const storageReady =
    storageMode === "local" || Boolean(state?.draft.storage_tested);
  const installViewState = installState ?? state;
  const installPhase = state?.phase ?? installViewState?.phase;
  const installPublicURL =
    installViewState?.draft.public_url ?? watchedConfig.public_url ?? "";
  const handoffReady =
    Boolean(handoffToken) &&
    (installPhase === "handoff" || installPhase === "complete");
  const callbackNotice = callbackStatus.includes("connected")
    ? "Connection saved. Continue when the status below is ready."
    : "";
  const callbackError = callbackStatus.includes("oauth-inactive")
    ? new Error(
        "Cloudflare OAuth is experimental and inactive. Use a scoped API token.",
      )
    : callbackStatus && !callbackStatus.includes("connected")
      ? new Error("The provider connection was not completed.")
      : undefined;
  const isPending =
    saveConfig.isPending ||
    saveManual.isPending ||
    saveCloudflareToken.isPending ||
    createCloudflareTunnel.isPending ||
    startAuthorization.isPending ||
    startInstall.isPending ||
    issueHandoff.isPending;

  return {
    accounts,
    actionError,
    activeStep,
    callbackError,
    callbackNotice,
    checks,
    checksPass,
    cloudflareConnected,
    cloudflareToken,
    configForm,
    createCloudflareTunnel,
    continueCloudflareSetup,
    dataReady,
    databaseMode,
    handoffReady,
    handoffToken,
    installPhase,
    installPublicURL,
    installViewState,
    isPending,
    manualForm,
    moveTo,
    networkMode,
    notice,
    ownerConfirmed,
    persistConfig,
    preflight,
    providerMode,
    redisMode,
    retryProductionInstall,
    saveConfig,
    saveManual,
    saveManualGitHub,
    saveCloudflareToken,
    selectedAccount,
    selectedZone,
    setActionError,
    sseConnected,
    setupCode,
    setupStatus,
    setupVerified,
    authorizeGitHubOwner,
    startAuthorization,
    startManifest,
    storageMode,
    storageReady,
    state,
    testDatabase,
    testDatabaseConnection,
    testRedis,
    testRedisConnection,
    testStorage,
    testStorageConnection,
    tunnelReady,
    verifyCode,
    verifySetupCode,
    watchedConfig,
    zones,
    connectCloudflareToken,
    setCloudflareAccountID,
    setCloudflareToken,
    setCloudflareZoneID,
    setProviderMode,
    setSetupCode,
    startProductionInstall,
  };
}
