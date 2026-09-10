import test from 'node:test';
import assert from 'node:assert/strict';
import { startStatisticsFixture } from './admin-statistics-fixture.mjs';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

test('statistics remain readable and interactive across content and viewport sizes', { timeout: 120000 }, async () => {
  const browser = await findBraveBrowser();
  assert.ok(browser, 'Brave is required for statistics layout verification');
  const fixture = await startStatisticsFixture();
  try {
    for (const [width, scenario, zoom] of [[320,'mixed'],[390,'actions'],[780,'crowded'],[1440,'representative'],[320,'empty'],[320,'large'],[390,'viewing'],[780,'mixed',2]]) {
      const rendered = await renderBraveDOM(browser, `${fixture.url}/?probe=1&scenario=${scenario}&zoom=${zoom || 1}`, { windowSize: `${width},900` });
      const match = rendered.stdout.match(/<pre id="fixtureResult" hidden="">([^<]+)<\/pre>/);
      assert.ok(match, `missing browser report: ${width}/${scenario}`);
      const result = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
      const label = `${width}/${scenario}/zoom=${zoom || 1}`;
      assert.deepEqual(result.errors, [], label);
      assert.equal(result.compactOverflow, false, label);
      assert.equal(result.metricClips, 0, label);
      assert.equal(result.dayWorks, true, label);
      assert.equal(result.tableWorks, true, label);
      assert.equal(result.initialMode, width <= 780 ? 'compact' : 'table', label);
      assert.equal(result.resources.length, 3, 'only two existing stylesheets and the statistics script');
      if (!['viewing','empty'].includes(scenario)) assert.ok(result.countDescriptions.every(text => /successful, \d+ accepted requests/.test(text)), label);
      console.log(`${label}: render=${result.renderMs.toFixed(1)}ms, payload=${result.payloadBytes}B, entry heights=${[...new Set(result.entryHeights)].join(',')}`);
    }
  } finally { await fixture.close(); }
});
