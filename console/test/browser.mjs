import assert from "node:assert/strict";
import { chromium } from "playwright";
import { fileURLToPath } from "node:url";
import { fixture, token } from "./fixture.mjs";

const cleanup = [];
const f = await fixture(
  { after: (callback) => cleanup.push(callback) },
  { dist: fileURLToPath(new URL("../dist", import.meta.url)) },
);
const browser = await chromium.launch({ headless: true });
try {
  const context = await browser.newContext();
  const page = await context.newPage();
  const failures = [];
  page.on("pageerror", (error) => failures.push(error.message));
  await page.goto(f.base);
  await page.getByRole("heading", { name: "Open your workspace" }).waitFor();
  assert.equal(f.calls.length, 0, "Unauthenticated browser must not contact Kubernetes");
  await page.getByLabel("Console access token").fill(token);
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await page.getByRole("heading", { name: "Current activity" }).waitFor();
  assert.equal(
    await page
      .locator(".card.stat")
      .first()
      .evaluate((element) => getComputedStyle(element).backgroundColor),
    "rgb(18, 18, 20)",
    "Exported cards retain the shared theme",
  );
  assert.equal(
    await page
      .locator(".muted")
      .first()
      .evaluate((element) => getComputedStyle(element).color),
    "rgb(154, 154, 163)",
    "Exported text retains theme contrast",
  );
  assert.equal(
    await page
      .getByRole("button", { name: "New run" })
      .evaluate((element) => getComputedStyle(element).backgroundColor),
    "rgb(232, 233, 237)",
    "Primary actions retain the shared theme",
  );
  assert.equal(
    await page
      .locator(".progress-cell")
      .first()
      .evaluate((element) => getComputedStyle(element).getPropertyValue("--color-steel").trim()),
    "#c8ccd4",
    "Progress retains its shared color token",
  );
  assert.equal(await page.getByLabel("cpu-training-02 progress").getAttribute("value"), "2");
  for (const [name, width, height] of [
    ["desktop", 1440, 900],
    ["short", 1024, 500],
    ["mobile", 390, 844],
    ["small", 320, 568],
  ]) {
    await page.setViewportSize({ width, height });
    await page.screenshot({
      path: `${process.env.ANVIL_SCREENSHOT_DIR || "/tmp"}/anvil-public-console-${name}.png`,
      fullPage: true,
    });
    assert.ok(
      await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
      `No horizontal overflow at ${name}`,
    );
    if (width < 768) {
      const more = page.getByRole("button", { name: "More", exact: true });
      await more.click();
      await page.getByRole("dialog", { name: "Navigation" }).waitFor();
      await page.keyboard.press("Escape");
      assert.equal(
        await more.evaluate((element) => element === document.activeElement),
        true,
        "More regains focus",
      );
      const menu = page.getByRole("button", { name: "Open navigation" });
      await menu.click();
      await page.keyboard.press("Escape");
      assert.equal(
        await menu.evaluate((element) => element === document.activeElement),
        true,
        "Header menu regains focus",
      );
    }
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const [id, label] of [
    ["trainingrecipes", "Recipes"],
    ["trainingruns", "Runs"],
    ["trainingartifacts", "Artifacts"],
    ["evaluationruns", "Evaluations"],
    ["modelpromotions", "Promotions"],
  ]) {
    await page.locator(".sidebar").getByRole("link", { name: label, exact: true }).click();
    await page.locator("h1").filter({ hasText: label }).waitFor();
    assert.equal(
      await page.locator('.sidebar a[aria-current="page"]').getAttribute("href"),
      `#${id}`,
    );
    assert.equal(await page.getByRole("button", { name: /approve|activate/i }).count(), 0);
    await page.locator(".resource-row .text-button").first().click();
    await page.getByRole("dialog", { name: "Resource details" }).waitFor();
    await page.keyboard.press("Escape");
  }
  await page.locator(".sidebar").getByRole("link", { name: "Runs", exact: true }).click();
  await page.getByRole("button", { name: "New run" }).click();
  await page.getByLabel("Run name", { exact: true }).fill("browser-run");
  await page.getByLabel("Recipe binding SHA-256").fill("0".repeat(64));
  await page.getByLabel("Parameters (JSON string values)", { exact: true }).fill('{"steps":"4"}');
  await page.getByLabel("I reviewed the recipe, digest, duration, and inputs.").check();
  await page.getByRole("button", { name: "Submit reviewed run", exact: true }).click();
  await page.getByRole("button", { name: "browser-run", exact: true }).waitFor();
  const write = f.calls.find((call) => call.method === "POST");
  assert.equal(write.path, "/apis/anvil.dev/v1alpha1/namespaces/anvil-test/trainingruns");
  assert.equal(write.body.spec.request.recipeRef.bindingSHA256, "0".repeat(64));
  const row = page
    .locator(".resource-row")
    .filter({ has: page.getByRole("button", { name: "cpu-training-02", exact: true }) });
  await row.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("button", { name: "Request cancellation", exact: true }).click();
  await row.getByText("Cancelled", { exact: true }).waitFor();
  assert.equal(f.calls.find((call) => call.method === "PATCH").body.metadata.uid, "demo-run-02");
  f.fail();
  await page.getByRole("button", { name: "Refresh resources" }).click();
  await page.getByRole("alert").filter({ hasText: "Kubernetes rejected" }).waitFor();
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.getByRole("heading", { name: "Open your workspace" }).waitFor();
  assert.equal(await page.getByRole("button", { name: "browser-run", exact: true }).count(), 0);
  assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0);
  assert.deepEqual(failures, []);
  console.log(
    "Console browser checks passed: auth, five resource pages, run submission, cancellation, four viewports, focus, and error handling",
  );
  await context.close();
} finally {
  await browser.close();
  for (const callback of cleanup.reverse()) await callback();
}
