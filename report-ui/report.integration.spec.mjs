// SPDX-License-Identifier: Apache-2.0
import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createServer } from 'node:http';
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const project = resolve(dirname(fileURLToPath(import.meta.url)), '..');
let artifacts, runDir, isolatedLocal, server, url;
let report;

test.beforeAll(async () => {
  test.setTimeout(180_000);
  artifacts = mkdtempSync(resolve(tmpdir(), 'benchstat-prof-browser-'));
  const manifest = resolve(artifacts, 'run-path.txt');
  // Run the actual CLI integration fixture, not mocked profile JSON.
  execFileSync('go', ['test', './integration', '-run', '^TestQuickSortDirectories$', '-count=1'], {
    cwd: project,
    env: { ...process.env, BENCHSTAT_PROF_INTEGRATION_DIR: artifacts, BENCHSTAT_PROF_INTEGRATION_MANIFEST: manifest },
    timeout: 150_000,
    stdio: 'pipe',
  });
  runDir = readFileSync(manifest, 'utf8');
  const web = readFileSync(resolve(runDir, 'report.html'), 'utf8');
  report = JSON.parse(web.split('<script type="application/json" id="report-data">')[1].split('</script>')[0]);
  // Only copy the standalone HTML: no sibling pprof files are available here.
  isolatedLocal = resolve(artifacts, 'isolated report-local.html');
  writeFileSync(isolatedLocal, readFileSync(resolve(runDir, 'report-local.html')));
  server = createServer((request, response) => {
    const path = resolve(runDir, '.' + decodeURIComponent(new URL(request.url, 'http://localhost').pathname));
    if (!path.startsWith(runDir + sep)) { response.writeHead(403).end(); return; }
    try {
      response.setHeader('Content-Type', path.endsWith('.html') ? 'text/html; charset=utf-8' : 'application/octet-stream');
      response.end(readFileSync(path));
    } catch { response.writeHead(404).end(); }
  });
  await new Promise(done => server.listen(0, '127.0.0.1', done));
  url = `http://127.0.0.1:${server.address().port}/report.html`;
});

test.afterAll(async () => {
  if (server) await new Promise(done => server.close(done));
  if (artifacts) rmSync(artifacts, { recursive: true, force: true });
});

test('web: profile load failures are shown to the user', async ({ page }) => {
  await page.route('**/*.pprof', route => route.fulfill({ status: 404, body: 'missing profile' }));
  await page.goto(url);
  const section = page.locator('#benchmark-0');
  await section.locator('summary').click();
  await expect(section.getByRole('alert')).toContainText('HTTP 404');
  await expect(section.getByTestId('flameGraph')).toHaveCount(0);
  // The evidence remains downloadable even if the viewer request fails.
  await expect(section.getByRole('link', { name: 'Download baseline pprof', exact: true })).toBeVisible();
});

for (const mode of ['web', 'local']) {
  test(`${mode}: real profiles, flamegraphs, overview, and downloads`, async ({ page }) => {
    const errors = [], requests = [];
    page.on('pageerror', error => errors.push(String(error)));
    page.on('request', request => requests.push(request.url()));
    if (mode === 'local') {
      // The standalone report must work offline, from file://, without evidence.
      await page.context().setOffline(true);
      await page.goto(pathToFileURL(isolatedLocal).href);
    } else await page.goto(url);
    await expect(page.getByRole('heading', { name: 'benchstat-prof report', exact: true })).toBeVisible();
    expect(report.summary.CPU).toBeTruthy();
    await expect(page.locator('.run-summary')).toContainText(`CPU: ${report.summary.CPU}`);
    await expect(page.locator('.overview tbody tr')).toHaveCount(report.benchmarks.length);
    await page.getByRole('button', { name: 'Sort by Comparison', exact: true }).click();
    await expect(page.locator('th[aria-sort="ascending"]')).toHaveCount(1);
    await page.getByLabel('Overview metric').selectOption('items/op');
    await expect(page.locator('.overview tbody tr')).toHaveCount(report.benchmarks.length);
    const customRows = await page.locator('.overview tbody tr').evaluateAll(rows => rows.map(row => Array.from(row.querySelectorAll('td')).slice(0, 2).map(cell => cell.textContent.trim())));
    for (const values of customRows) {
      expect(values[0]).not.toBe('—');
      expect(values[0]).toBe(values[1]);
    }
    await page.getByLabel('Overview metric').selectOption('profile:allocations');
    await expect(page.locator('.overview tbody tr')).toHaveCount(report.benchmarks.length);

    for (let bi = 0; bi < report.benchmarks.length; bi++) {
      const benchmark = report.benchmarks[bi];
      const section = page.locator(`#benchmark-${bi}`);
      await section.locator('summary').click();
      for (const profile of benchmark.profiles) {
        await section.getByLabel(`${benchmark.name} profile`, { exact: true }).selectOption({ label: profile.name });
        await expect(section.getByTestId('flameGraph').first()).toBeVisible();
        await expect(section.getByRole('alert')).toHaveCount(0);
        const canvas = await section.getByTestId('flameGraph').first().evaluate(element => ({ width: element.width, height: element.height }));
        expect(canvas.width).toBeGreaterThan(0);
        expect(canvas.height).toBeGreaterThan(0);
        for (const side of ['baseline', 'comparison']) {
          // Chromium rate-limits more than ten downloads in a second.
          await page.waitForTimeout(150);
          const promised = page.waitForEvent('download');
          await section.getByRole('link', { name: `Download ${side} pprof`, exact: true }).click();
          const download = await promised;
          expect(download.suggestedFilename()).toBe(`${side}-${benchmark.name.replace(/[^a-zA-Z0-9._-]+/g, '-')}-${profile.name}.pprof`);
          expect(await download.failure()).toBeNull();
          const actual = readFileSync(await download.path());
          const expected = readFileSync(resolve(runDir, profile[`${side}File`]));
          expect(actual.equals(expected)).toBe(true);
        }
      }
    }
    expect(errors).toEqual([]);
    if (mode === 'web') {
      expect(requests.filter(request => request.endsWith('.pprof')).length).toBeGreaterThanOrEqual(report.benchmarks.length * 4);
    } else {
      expect(requests.filter(request => /^https?:/.test(request))).toEqual([]);
      expect(requests.filter(request => request.endsWith('.pprof'))).toEqual([]);
    }
  });
}
