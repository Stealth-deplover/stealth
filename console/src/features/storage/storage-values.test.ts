import { describe, expect, it } from "vitest";
import { bucketName, objectName, positiveBytes } from "./storage-values";

describe("storage input boundaries", () => {
  it("preserves a display filename and rejects path or header injection", () => {
    expect(objectName.parse("avatar.png")).toBe("avatar.png");
    for (const name of [
      "../avatar.png",
      "dir/file.txt",
      "dir\\file.txt",
      "bad\r\nname",
      ".",
      "..",
    ])
      expect(objectName.safeParse(name).success).toBe(false);
  });
  it("validates bucket names and positive integral size limits", () => {
    expect(bucketName.parse("app-assets")).toBe("app-assets");
    expect(bucketName.safeParse("App Assets").success).toBe(false);
    expect(positiveBytes("1024")).toBe(1024);
    for (const value of ["0", "-1", "1.5", "NaN", "9007199254740992"])
      expect(() => positiveBytes(value)).toThrow();
  });
});
