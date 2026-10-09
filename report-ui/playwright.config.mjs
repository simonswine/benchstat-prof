// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: 'report.integration.spec.mjs',
  workers: 1,
  timeout: 60_000,
  use: { browserName: 'chromium', acceptDownloads: true, trace: 'retain-on-failure' },
});
