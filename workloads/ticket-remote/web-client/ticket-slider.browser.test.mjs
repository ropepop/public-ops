import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

// Synthetic card pixels only. This exercises the actual painter and browser
// fonts without connecting to Ticket, using a saved profile or touching a phone.
async function probe(paintTicketSlider) {
  const checks = [], errors = [];
  const check = (label, ok) => { if (!ok) throw Error(label); checks.push(label); };
  const width = 1080, height = 1920;
  const region = { leftBasisPoints: 685, topBasisPoints: 7000, rightBasisPoints: 9315, bottomBasisPoints: 7766 };
  const x = width * region.leftBasisPoints / 10000, y = height * region.topBasisPoints / 10000;
  const w = width * (region.rightBasisPoints - region.leftBasisPoints) / 10000;
  const h = height * (region.bottomBasisPoints - region.topBasisPoints) / 10000;
  const raw = document.createElement('canvas'), output = document.createElement('canvas');
  for (const canvas of [raw, output]) { canvas.width = width; canvas.height = height; }
  const source = raw.getContext('2d'), ctx = output.getContext('2d', { willReadFrequently: true });
  const proof = document.createElement('canvas'); proof.width = 2700; proof.height = 420;
  const proofContext = proof.getContext('2d');
  proofContext.fillStyle = '#fff'; proofContext.fillRect(0, 0, proof.width, proof.height);
  const image = () => ctx.getImageData(0, 0, width, height).data;
  const same = (a, b) => a.length === b.length && a.every((v, i) => v === b[i]);
  const pixel = (data, px, py) => Array.from(data.slice((Math.floor(py) * width + Math.floor(px)) * 4, (Math.floor(py) * width + Math.floor(px)) * 4 + 4));
  const rgb = (data, px, py, value) => same(pixel(data, px, py), [...value, 255]);
  const state = (extra = {}) => ({ region, state: 'ready', offset: 0, reducedMotion: true, ...extra });
  const paint = (slider, time = 0) => { paintTicketSlider(ctx, raw, width, height, slider, time); return image(); };
  try {
    await Promise.all([document.fonts.load('600 42px TicketSlider', 'Reģistrēt biļeti'), document.fonts.load('320 35px TicketSlider', 'Pavelc, lai apstiprinātu')]);
    check('both native slider fonts load', document.fonts.check('600 42px TicketSlider') && document.fonts.check('320 35px TicketSlider'));
    const textCalls = [], nativeText = ctx.fillText.bind(ctx);
    ctx.fillText = (...args) => { textCalls.push({ text: args[0], font: ctx.font, x: args[1], baseline: args[2] }); nativeText(...args); };
    for (const [row, shade] of [255, 221, 85].entries()) {
      source.fillStyle = `rgb(${shade} ${shade} ${shade})`; source.fillRect(0, 0, width, height);
      source.fillStyle = '#0080ff'; source.fillRect(20, 20, 30, 30);
      const cleanCard = source.getImageData(0, 0, width, height).data;
      // Strongly different original pixels extend into the rounded corners and
      // classifier margin. A constant white mask would fail two of these cards.
      source.fillStyle = '#ff00ff'; source.fillRect(Math.floor(x) - 3, Math.floor(y) - 3, Math.ceil(w) + 6, Math.ceil(h) + 6);
      const before = source.getImageData(0, 0, width, height).data;
      check(`raw ${shade} card contains the delayed native marker`, rgb(before, x, y, [255, 0, 255]));
      const startText = textCalls.length;
      const ready = paint(state());
      check(`${shade} card keeps pixels outside the replacement`, rgb(ready, 30, 30, [0, 128, 255]) && rgb(ready, 40, y + h / 2, [shade, shade, shade]));
      check(`${shade} rounded corners and margin use same-frame card shading`, [[x, y], [x + w - 1, y], [x, y + h - 1], [x + w - 1, y + h - 1], [x - 3, y + h / 2]].every(([px, py]) => rgb(ready, px, py, [shade, shade, shade])));
      let oldPixels = 0, orangeOutside = 0, orangePixels = 0;
      for (let py = Math.floor(y) - 8; py < Math.ceil(y + h) + 8; py++) {
        for (let px = Math.floor(x) - 7; px < Math.ceil(x + w) + 7; px++) {
          const at = (py * width + px) * 4;
          if (ready[at] > 240 && ready[at + 1] < 20 && ready[at + 2] > 240) oldPixels++;
          if (ready[at] === 247 && ready[at + 1] === 181 && ready[at + 2] === 0) {
            orangePixels++;
            if (px < Math.floor(x) || px > Math.ceil(x + w) || py < Math.floor(y) || py > Math.ceil(y + h)) orangeOutside++;
          }
        }
      }
      check(`${shade} card has one native-size replacement with no original marker`, oldPixels === 0 && orangeOutside === 0 && orangePixels > w * h * 0.55);
      const copy = textCalls.slice(startText);
      check(`${shade} slider uses the two native text lines at measured sizes`, copy.length === 2 && copy[0].text === 'Reģistrēt biļeti' && copy[1].text === 'Pavelc, lai apstiprinātu' && /^600 42\./.test(copy[0].font) && /^320 35\./.test(copy[1].font) && Math.abs(copy[0].x - 466) < 1 && Math.abs(copy[1].x - 466) < 1);
      check(`${shade} native thumb has the measured resting diameter`, rgb(ready, x + h / 2, y + h * 0.2, [38, 43, 47]) && rgb(ready, x + h + 8, y + h / 2, [247, 181, 0]));
      proofContext.drawImage(output, 0, y - 45, width, h + 90, 0, row * 140, 540, 118);
      const dragging = paint(state({ state: 'dragging', offset: 0.5 }));
      check(`${shade} drag moves the same thumb and reveals card behind it`, rgb(dragging, x + w / 2, y + h * 0.2, [38, 43, 47]) && rgb(dragging, x + h / 2, y + h / 2, [shade, shade, shade]));
      proofContext.drawImage(output, 0, y - 45, width, h + 90, 540, row * 140, 540, 118);
      for (const offset of [0.25, 0.5, 1]) {
        const moved = paint(state({ state: 'dragging', offset })), edge = x + (w - h) * offset;
        check(`${shade} track at ${offset} follows the thumb with a fixed right edge`,
          rgb(moved, edge - 2, y + h / 2, [shade, shade, shade]) &&
          rgb(moved, edge + 1, y + 1, [shade, shade, shade]) &&
          rgb(moved, edge + 2, y + h / 2, [247, 181, 0]) &&
          rgb(moved, edge + h / 2, y + h * 0.2, [38, 43, 47]) &&
          rgb(moved, x + w - 2, y + h / 2, [247, 181, 0]));
      }
      proofContext.drawImage(output, 0, y - 45, width, h + 90, 1620, row * 140, 540, 118);
      check(`${shade} cancellation restores the exact ready picture`, same(paint(state()), ready));
      const returning = state({ resetFrom: 0.5, resetAt: 3000, reducedMotion: false });
      const resetStart = paint(returning, 3000), resetMiddle = paint(returning, 3050);
      check(`${shade} cancelled swipe returns through an intermediate picture`, !same(resetStart, resetMiddle) && !same(resetMiddle, ready) && same(paint(returning, 3200), paint(state({ reducedMotion: false }), 3200)));
      check(`${shade} returning track regrows behind the thumb`,
        rgb(resetStart, x + w / 3, y + h / 2, [shade, shade, shade]) &&
        !rgb(resetMiddle, x + w / 3, y + h / 2, [shade, shade, shade]) &&
        rgb(resetMiddle, x + h / 2, y + h / 2, [shade, shade, shade]));
      paint(returning, 3050);
      proofContext.drawImage(output, 0, y - 45, width, h + 90, 1080, row * 140, 540, 118);
      check(`${shade} dismissing the replacement restores untouched raw pixels`, same(paint(null), before));
      const cover = state({ state: 'cover', offset: 0.5, resetFrom: 0.5, resetAt: 3000, reducedMotion: false });
      check(`${shade} cover leaves only the card, preserves outside pixels and never paints a wave`,
        same(paint(cover, 1125), cleanCard) && same(paint(cover, 3050), cleanCard));
      proofContext.drawImage(output, 0, y - 45, width, h + 90, 2160, row * 140, 540, 118);
      check(`${shade} dismissing the cover restores untouched raw pixels`, same(paint(null), before));
      proofContext.fillStyle = '#111'; proofContext.font = '14px sans-serif';
      for (const [column, label] of ['Ready', 'Dragging', 'Returning', 'Complete', 'Covered'].entries()) proofContext.fillText(`${label} · card #${shade.toString(16).repeat(3)}`, column * 540 + 15, row * 140 + 133);
    }
    const still = paint(state({ reducedMotion: false }), 0);
    const pulse = paint(state({ reducedMotion: false }), 1125);
    check('passive wave changes the composed picture and repeats every three seconds', !same(still, pulse) && same(still, paint(state({ reducedMotion: false }), 3000)));
    check('wave remains inside the native pill', rgb(pulse, x, y, [85, 85, 85]) && rgb(pulse, x - 3, y + h / 2, [85, 85, 85]));
    check('reduced motion disables the passive wave', same(paint(state(), 0), paint(state(), 1125)));
    check('reduced motion immediately resets a cancelled swipe', same(paint(state({ resetFrom: 0.5, resetAt: 3000 }), 3000), paint(state(), 3000)));
    const expected = paint(state()), frame = new VideoFrame(raw, { timestamp: 1000 });
    try {
      paintTicketSlider(ctx, frame, width, height, state(), 0);
      check('decoded video frames use the same exact backing and slider composition', same(image(), expected));
    } finally { frame.close(); }

    // WebKit can ignore a VideoFrame source crop and squeeze the whole frame
    // into the destination. A flat full-frame fixture hid the resulting border.
    source.fillStyle = '#303b3d'; source.fillRect(0, 0, width, height);
    source.fillStyle = '#fff'; source.fillRect(40, 100, width - 80, height - 200);
    source.fillStyle = '#ff00ff'; source.fillRect(Math.floor(x) - 3, Math.floor(y) - 3, Math.ceil(w) + 6, Math.ceil(h) + 6);
    const nativeDrawImage = ctx.drawImage.bind(ctx);
    ctx.drawImage = (...args) => args[0] instanceof VideoFrame && args.length === 9
      ? nativeDrawImage(args[0], ...args.slice(5)) : nativeDrawImage(...args);
    try {
      for (const [label, init] of [
        ['native-size', {}],
        ['cropped and scaled', { visibleRect: { x: 40, y: 80, width: 1000, height: 1760 }, displayWidth: 500, displayHeight: 880 }]
      ]) {
        const video = new VideoFrame(raw, { timestamp: 2000, ...init });
        try {
          const displayed = document.createElement('canvas'); displayed.width = width; displayed.height = height;
          displayed.getContext('2d').drawImage(video, 0, 0, width, height);
          paintTicketSlider(ctx, displayed, width, height, state(), 0);
          const expectedVideo = image();
          paintTicketSlider(ctx, video, width, height, state(), 0);
          check(`${label} video crop quirk preserves the white card border and surrounding picture`, same(image(), expectedVideo));
        } finally { video.close(); }
      }
    } finally { ctx.drawImage = nativeDrawImage; }
  } catch (error) { errors.push(String(error)); }
  const result = document.createElement('pre'); result.id = 'sliderResult'; result.hidden = true;
  result.textContent = JSON.stringify({ checks, errors }); document.body.append(result);
  const preview = document.createElement('img'); preview.id = 'sliderPreview'; preview.src = proof.toDataURL('image/png'); document.body.append(preview);
  document.documentElement.dataset.probeComplete = 'true';
}

test('slider pixels share the ticket card backing, typography and animation path', { timeout: 45000 }, async () => {
  const browser = await findBraveBrowser(); assert.ok(browser, 'Brave is required for the loopback test');
  const bundle = await build({ stdin: { contents: `import { paintTicketSlider } from './ticket-slider-painter.mjs'; (${probe.toString()})(paintTicketSlider);`, resolveDir: new URL('.', import.meta.url).pathname }, bundle: true, format: 'iife', write: false, logLevel: 'silent' });
  const root = new URL('../internal/web/static/', import.meta.url);
  const server = createServer(async (req, res) => {
    const pathname = new URL(req.url, 'http://127.0.0.1').pathname;
    if (pathname === '/') { res.setHeader('Content-Type', 'text/html'); res.end('<!doctype html><style>@font-face{font-family:TicketSlider;font-weight:320;src:url(/ticket-slider-light.woff2)}@font-face{font-family:TicketSlider;font-weight:600;src:url(/ticket-slider-semibold.woff2)}body{margin:0}img{max-width:100%}</style><script defer src="/probe.js"></script>'); return; }
    if (pathname === '/probe.js') { res.setHeader('Content-Type', 'text/javascript'); res.end(bundle.outputFiles[0].text); return; }
    if (['/ticket-slider-light.woff2', '/ticket-slider-semibold.woff2'].includes(pathname)) { res.setHeader('Content-Type', 'font/woff2'); res.end(await readFile(new URL(pathname.slice(1), root))); return; }
    res.writeHead(404); res.end();
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const rendered = await renderBraveDOM(browser, `http://127.0.0.1:${server.address().port}`, { timeoutMillis: 30000, windowSize: '1440,900' });
    const match = rendered.stdout.match(/<pre id="sliderResult" hidden="">([^<]+)<\/pre>/); assert.ok(match, 'missing slider pixel report');
    const result = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
    if (process.env.TICKET_SLIDER_PROOF_PATH) {
      const image = rendered.stdout.match(/id="sliderPreview" src="data:image\/png;base64,([^"]+)"/); assert.ok(image, 'missing slider preview');
      await writeFile(process.env.TICKET_SLIDER_PROOF_PATH, Buffer.from(image[1], 'base64'));
    }
    assert.deepEqual(result.errors, [], JSON.stringify(result));
    assert.equal(result.checks.length, 56);
    console.log(JSON.stringify(result));
  } finally { await new Promise(resolve => server.close(resolve)); }
});
