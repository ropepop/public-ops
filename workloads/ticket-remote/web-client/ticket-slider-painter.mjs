// The replacement belongs to the picture, so card, lettering and animation all
// take the same SDR/HDR path. The raw source remains owned by Presentation.
export function paintTicketSlider(ctx, source, width, height, slider, time = 0) {
  ctx.drawImage(source, 0, 0, width, height);
  if (!slider) return;
  const r = slider.region;
  const x = width * r.leftBasisPoints / 10000, y = height * r.topBasisPoints / 10000;
  const w = width * (r.rightBasisPoints - r.leftBasisPoints) / 10000;
  const h = height * (r.bottomBasisPoints - r.topBasisPoints) / 10000;
  if (!(w > h && h > 0)) return;

  // The phone refines the native bounds on a 192 x 288 probe. Erase one
  // boundary sample, including the rounded corners, with the card's own pixels.
  // ViVi's card is flat here; sample across its width to preserve any shading.
  const left = Math.max(0, Math.floor(x - width / 192));
  const top = Math.max(1, Math.floor(y - height / 288));
  const right = Math.min(width, Math.ceil(x + w + width / 192));
  const bottom = Math.min(height, Math.ceil(y + h + height / 288));
  // WebKit ignores the source crop for VideoFrame draws. Sample the full frame
  // already painted above, in its final coordinates, instead of cropping video.
  ctx.drawImage(ctx.canvas, left, top - 1, right - left, 1,
    left, top, right - left, bottom - top);
  if (slider.state === 'cover') return;

  const reset = Math.max(0, Math.min(1, (time - (slider.resetAt || 0)) / 200));
  const returning = slider.reducedMotion ? 0 : (slider.resetFrom || 0) * (1 - reset) ** 3;
  const progress = Math.max(0, Math.min(1, (Number(slider.offset) || 0) + returning));
  const inset = (w - h) * progress;
  ctx.save();
  ctx.translate(x, y);
  ctx.beginPath();
  ctx.roundRect(inset, 0, w - inset, h, h / 2);
  ctx.clip();
  ctx.fillStyle = '#f7b500';
  ctx.fillRect(0, 0, w, h);
  ctx.globalAlpha = 1 - progress;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'alphabetic';
  ctx.fillStyle = '#2e3438';
  ctx.font = `600 ${h * 0.285714}px TicketSlider, sans-serif`;
  ctx.letterSpacing = `${h * 0.0051}px`;
  ctx.fillText('Reģistrēt biļeti', w / 2, h * 0.428571);
  ctx.fillStyle = '#424133';
  ctx.font = `320 ${h * 0.238095}px TicketSlider, sans-serif`;
  ctx.letterSpacing = `${h * 0.00446}px`;
  ctx.fillText('Pavelc, lai apstiprinātu', w / 2, h * 0.77551);
  ctx.globalAlpha = 1;
  const radius = h * 0.459184, cx = h / 2 + inset, cy = h / 2;
  ctx.fillStyle = '#262b2f';
  ctx.beginPath();
  ctx.arc(cx, cy, radius, 0, Math.PI * 2);
  ctx.fill();
  ctx.save();
  ctx.translate(cx, cy);
  const size = h * 0.375;
  ctx.scale(size / 24, size / 24);
  ctx.translate(-12, -12);
  ctx.fillStyle = '#fff';
  ctx.fill(new Path2D('m12 4-1.41 1.41L16.17 11H4v2h12.17l-5.58 5.59L12 20l8-8z'));
  ctx.restore();
  if (!slider.reducedMotion && slider.state === 'ready') {
    const phase = ((time % 3000) + 3000) % 3000 / 3000;
    const travel = Math.min(1, phase / 0.75);
    const eased = travel * travel * (3 - 2 * travel);
    const center = w * (-0.5 + 2 * eased);
    const half = w * 0.375;
    const wave = ctx.createLinearGradient(center - half, 0, center + half, h);
    const alpha = 0.19 * Math.min(1, phase / 0.15, (1 - phase) / 0.25);
    wave.addColorStop(0, 'rgba(255,255,255,0)');
    wave.addColorStop(0.5, `rgba(255,255,255,${alpha})`);
    wave.addColorStop(1, 'rgba(255,255,255,0)');
    ctx.fillStyle = wave;
    ctx.fillRect(0, 0, w, h);
  }
  ctx.restore();
}
