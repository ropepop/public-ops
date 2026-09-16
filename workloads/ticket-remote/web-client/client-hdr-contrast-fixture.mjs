// Synthetic only: shared by the owner comparison and real-GPU checks.
export const HDR_CONTRAST_GRAYS = Object.freeze([216, 224, 232, 240, 244, 248, 252, 254]);

export function drawHDRContrastFixture(canvas) {
  canvas.width = 360;
  canvas.height = 300;
  const context = canvas.getContext('2d', { alpha: false, colorSpace: 'srgb' });
  context.fillStyle = '#fff';
  context.fillRect(0, 0, canvas.width, canvas.height);
  ['#8d2c25', '#e68206', '#32b450', '#3264c8', '#000'].forEach((color, index) => {
    context.fillStyle = color;
    context.fillRect(index * 72, 0, 72, 36);
  });
  context.font = '12px sans-serif';
  HDR_CONTRAST_GRAYS.forEach((gray, index) => {
    const y = 48 + index * 30;
    context.fillStyle = `rgb(${gray} ${gray} ${gray})`;
    context.fillRect(12, y, 24, 20);
    context.fillText(`Pale footer text ${gray} — Aa 0123456789`, 46, y + 14);
  });
  return canvas;
}
