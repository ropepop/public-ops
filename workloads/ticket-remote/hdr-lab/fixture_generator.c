#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

enum { WIDTH = 768, HEIGHT = 512 };

static uint16_t float_to_half(float value) {
  union {
    float f;
    uint32_t u;
  } bits = {.f = value};
  const uint32_t sign = (bits.u >> 16) & 0x8000u;
  int32_t exponent = (int32_t)((bits.u >> 23) & 0xffu) - 127 + 15;
  uint32_t mantissa = bits.u & 0x7fffffu;

  if (exponent <= 0) {
    if (exponent < -10) return (uint16_t)sign;
    mantissa = (mantissa | 0x800000u) >> (1 - exponent);
    return (uint16_t)(sign | ((mantissa + 0x1000u) >> 13));
  }
  if (exponent >= 31) return (uint16_t)(sign | 0x7c00u);
  return (uint16_t)(sign | ((uint32_t)exponent << 10) | ((mantissa + 0x1000u) >> 13));
}

static float clamp01(float value) {
  if (value < 0.0f) return 0.0f;
  if (value > 1.0f) return 1.0f;
  return value;
}

static float srgb_to_linear(float value) {
  value = clamp01(value);
  return value <= 0.04045f ? value / 12.92f : powf((value + 0.055f) / 1.055f, 2.4f);
}

static void synthetic_pixel(int x, int y, float *r, float *g, float *b) {
  const float nx = (float)x / (float)(WIDTH - 1);
  const float ny = (float)y / (float)(HEIGHT - 1);

  *r = 0.025f + 0.10f * nx;
  *g = 0.030f + 0.08f * ny;
  *b = 0.045f + 0.10f * (1.0f - nx);

  if (y >= 38 && y < 150) {
    static const float bars[8][3] = {
        {0.18f, 0.18f, 0.18f}, {0.35f, 0.08f, 0.08f}, {0.08f, 0.35f, 0.08f},
        {0.08f, 0.12f, 0.38f}, {0.48f, 0.30f, 0.06f}, {0.38f, 0.08f, 0.42f},
        {0.04f, 0.40f, 0.42f}, {0.72f, 0.72f, 0.72f},
    };
    const int bar = (x * 8) / WIDTH;
    *r = bars[bar][0];
    *g = bars[bar][1];
    *b = bars[bar][2];
  }

  if (y >= 190 && y < 292 && x >= 42 && x < WIDTH - 42) {
    const float ramp = (float)(x - 42) / (float)(WIDTH - 85);
    *r = ramp;
    *g = ramp;
    *b = ramp;
  }

  const float dx = (float)x - WIDTH * 0.25f;
  const float dy = (float)y - HEIGHT * 0.79f;
  const float distance = sqrtf(dx * dx + dy * dy);
  if (distance < 70.0f) {
    const float core = clamp01(1.0f - distance / 70.0f);
    *r = 0.20f + 0.80f * core;
    *g = 0.12f + 0.88f * core;
    *b = 0.05f + 0.95f * core;
  }

  const float hx = (float)x - WIDTH * 0.71f;
  const float hy = (float)y - HEIGHT * 0.79f;
  if (fabsf(hx) < 115.0f && fabsf(hy) < 55.0f) {
    const float edge = clamp01(1.0f - fmaxf(fabsf(hx) / 115.0f, fabsf(hy) / 55.0f));
    *r = 0.15f + 0.85f * edge;
    *g = 0.20f + 0.80f * edge;
    *b = 0.40f + 0.60f * edge;
  }

  if ((x % 96 == 0) || (y % 64 == 0)) {
    *r *= 0.55f;
    *g *= 0.55f;
    *b *= 0.55f;
  }

  *r = clamp01(*r);
  *g = clamp01(*g);
  *b = clamp01(*b);
}

int main(int argc, char **argv) {
  if (argc != 3) {
    fprintf(stderr, "usage: %s SDR_RGBA8888.raw HDR_RGBA16F.raw\n", argv[0]);
    return 2;
  }

  FILE *sdr = fopen(argv[1], "wb");
  FILE *hdr = fopen(argv[2], "wb");
  if (sdr == NULL || hdr == NULL) {
    perror("open output");
    if (sdr != NULL) fclose(sdr);
    if (hdr != NULL) fclose(hdr);
    return 1;
  }

  for (int y = 0; y < HEIGHT; ++y) {
    for (int x = 0; x < WIDTH; ++x) {
      float r, g, b;
      synthetic_pixel(x, y, &r, &g, &b);

      const uint8_t sdr_pixel[4] = {
          (uint8_t)lroundf(r * 255.0f), (uint8_t)lroundf(g * 255.0f),
          (uint8_t)lroundf(b * 255.0f), 255u,
      };
      const uint16_t hdr_pixel[4] = {
          float_to_half(4.0f * srgb_to_linear(r)),
          float_to_half(4.0f * srgb_to_linear(g)),
          float_to_half(4.0f * srgb_to_linear(b)),
          float_to_half(1.0f),
      };

      if (fwrite(sdr_pixel, sizeof(sdr_pixel), 1, sdr) != 1 ||
          fwrite(hdr_pixel, sizeof(hdr_pixel), 1, hdr) != 1) {
        perror("write output");
        fclose(sdr);
        fclose(hdr);
        return 1;
      }
    }
  }

  if (fclose(sdr) != 0 || fclose(hdr) != 0) {
    perror("close output");
    return 1;
  }
  return 0;
}
