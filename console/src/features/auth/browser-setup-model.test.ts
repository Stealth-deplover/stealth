import { describe, expect, it } from "vitest";
import {
  configFromState,
  defaultConfig,
  setupCodeSchema,
  toSetupRequest,
  type SetupState,
} from "@/features/auth/browser-setup-model";

describe("browser setup model", () => {
  it("hydrates only public draft choices and keeps credential inputs empty", () => {
    const state = {
      draft: {
        instance_name: "Production",
        public_url: "https://console.example.test",
        network_mode: "local_only",
        database_mode: "external",
        redis_mode: "external",
        storage_mode: "s3",
        storage_s3_region: "eu-west-1",
      },
    } as SetupState;

    expect(configFromState(state)).toMatchObject({
      instance_name: "Production",
      public_url: "https://console.example.test",
      network_mode: "local_only",
      database_mode: "external",
      redis_mode: "external",
      storage_mode: "s3",
      storage_s3_region: "eu-west-1",
      database_url: "",
      redis_url: "",
      storage_s3_access_key: "",
      storage_s3_secret_key: "",
    });
  });

  it("converts empty optional fields to omitted API inputs", () => {
    const request = toSetupRequest(defaultConfig);

    expect(request).toMatchObject({
      instance_name: "Stealth",
      public_url: "http://localhost:8081",
      network_mode: "cloudflare_tunnel",
      database_mode: "bundled",
      redis_mode: "bundled",
      storage_mode: "local",
      storage_s3_use_ssl: true,
      storage_s3_path_style: true,
    });
    expect(request.database_url).toBeUndefined();
    expect(request.redis_url).toBeUndefined();
    expect(request.storage_s3_secret_key).toBeUndefined();
  });

  it("normalizes valid setup codes before they reach the API", () => {
    expect(setupCodeSchema.parse("stealth-abcd-2345-efgh")).toBe(
      "STEALTH-ABCD-2345-EFGH",
    );
    expect(setupCodeSchema.safeParse("STEALTH-AAAA-1111-OOOO").success).toBe(
      false,
    );
  });
});
