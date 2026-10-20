import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

// Check the actual light-theme values shipped by this service, without requiring
// the shared submodule in the frontend-only CI/Docker context. This is not a
// substitute for computed-style, focus, zoom or screen-reader browser checks.
const shared = readFileSync(new URL("../styles/vendor/xheize-design/tokens.css", import.meta.url), "utf8");
const adapter = readFileSync(new URL("../styles/theme.css", import.meta.url), "utf8");
const palette = new Map();
for (const css of [shared, adapter]) {
  for (const [, selector, declarations] of css.matchAll(/([^{}]+)\{([^{}]+)\}/g)) {
    if (!selector.includes(":root") && !selector.includes('[data-x-product="quanyejun"][data-x-theme="light"]')) continue;
    for (const [, key, value] of declarations.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) palette.set(key, value.trim());
  }
}
function color(token) {
  let value = palette.get(token);
  for (let depth = 0; value?.startsWith("var(") && depth < 10; depth++) value = palette.get(value.slice(4, -1));
  assert.match(value ?? "", /^#[\da-f]{6}$/i, token);
  return value;
}
function luminance(hex) {
  const rgb = hex.slice(1).match(/../g).map(part => {
    const c = parseInt(part, 16) / 255;
    return c <= .04045 ? c / 12.92 : ((c + .055) / 1.055) ** 2.4;
  });
  return rgb[0] * .2126 + rgb[1] * .7152 + rgb[2] * .0722;
}
function check(foreground, background, minimum) {
  const values = [luminance(color(foreground)), luminance(color(background))].sort((a, b) => b - a);
  const ratio = (values[0] + .05) / (values[1] + .05);
  assert.ok(ratio >= minimum, `${foreground} / ${background}: ${ratio.toFixed(2)} < ${minimum}`);
}
test("service text and status colors meet 4.5:1 on their light surfaces", () => {
  for (const surface of ["--x-surface", "--x-surface-soft", "--x-canvas"]) {
    check("--x-text", surface, 4.5);
    check("--x-muted", surface, 4.5);
    check("--x-success", surface, 4.5);
    check("--x-danger", surface, 4.5);
  }
  for (const state of ["success", "warning", "danger"]) check(`--x-${state}`, `--x-${state}-soft`, 4.5);
  check("--x-on-action", "--x-action", 4.5);
  check("--x-on-action", "--x-action-hover", 4.5);
  check("--q-accent", "--x-action", 4.5);
  check("--q-on-accent", "--q-accent", 4.5);
});
test("control borders and focus indicators meet 3:1 on adjacent surfaces", () => {
  for (const surface of ["--x-surface", "--x-surface-soft", "--x-canvas"]) {
    check("--x-border-control", surface, 3);
    check("--x-focus", surface, 3);
  }
});
