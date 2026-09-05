import assert from "node:assert/strict";
import test from "node:test";
import { defaultView, hasExplorer } from "./navigation.ts";

test("desktop opens on overview", () => {
  assert.equal(defaultView, "overview");
});

test("overview uses the full workbench without an Explorer sidebar", () => {
  assert.equal(hasExplorer("overview"), false);
});

test("resource and management views keep their Explorer sidebar", () => {
  for (const view of ["clusters", "workload", "network", "sessions", "settings"]) {
    assert.equal(hasExplorer(view), true, view);
  }
});

test("standalone utility views do not expose an empty Explorer sidebar", () => {
  assert.equal(hasExplorer("host-aliases"), false);
  assert.equal(hasExplorer("mcp"), false);
});
