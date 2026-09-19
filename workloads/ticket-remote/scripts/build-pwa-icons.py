"""Generate the simple Ticket home-screen artwork (requires Pillow)."""
from pathlib import Path
from PIL import Image, ImageDraw

output = Path(__file__).resolve().parents[1] / "internal/web/pwa"
image = Image.new("RGB", (1024, 1024), "#020304")
draw = ImageDraw.Draw(image)
# Keep all meaningful artwork within the maskable icon's central safe circle.
draw.rounded_rectangle((224, 324, 800, 700), radius=54, fill="#c6e2ff")
for x in (224, 800):
    draw.ellipse((x - 42, 470, x + 42, 554), fill="#020304")
for y in range(368, 658, 58):
    draw.rounded_rectangle((626, y, 640, y + 28), radius=7, fill="#263b52")
draw.rounded_rectangle((318, 424, 550, 448), radius=12, fill="#263b52")
draw.rounded_rectangle((318, 480, 510, 504), radius=12, fill="#263b52")
draw.rounded_rectangle((318, 574, 466, 592), radius=9, fill="#263b52")
for name, size in [("icon-192.png", 192), ("icon-512.png", 512),
                   ("icon-maskable.png", 512), ("apple-touch-icon.png", 180)]:
    image.resize((size, size), Image.Resampling.LANCZOS).save(output / name, optimize=True)
