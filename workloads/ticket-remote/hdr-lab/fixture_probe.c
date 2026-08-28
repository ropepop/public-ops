#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <ultrahdr_api.h>

enum { BASE_WIDTH = 768, BASE_HEIGHT = 512, GAIN_WIDTH = 384, GAIN_HEIGHT = 256 };

typedef struct file_bytes {
  uint8_t *data;
  size_t size;
} file_bytes_t;

static int load_file(const char *path, file_bytes_t *file) {
  FILE *stream = fopen(path, "rb");
  if (stream == NULL) return 0;
  if (fseek(stream, 0, SEEK_END) != 0) goto fail;
  long size = ftell(stream);
  if (size <= 0 || fseek(stream, 0, SEEK_SET) != 0) goto fail;
  file->data = malloc((size_t)size);
  if (file->data == NULL) goto fail;
  file->size = (size_t)size;
  if (fread(file->data, file->size, 1, stream) != 1) {
    free(file->data);
    file->data = NULL;
    file->size = 0;
    goto fail;
  }
  fclose(stream);
  return 1;

fail:
  fclose(stream);
  return 0;
}

static int contains_bytes(const file_bytes_t *file, const char *needle) {
  const size_t length = strlen(needle);
  if (length == 0 || file->size < length) return 0;
  for (size_t i = 0; i <= file->size - length; ++i) {
    if (memcmp(file->data + i, needle, length) == 0) return 1;
  }
  return 0;
}

static int nearly(float actual, float expected, float tolerance) {
  return fabsf(actual - expected) <= tolerance;
}

int main(int argc, char **argv) {
  if (argc != 3) {
    fprintf(stderr, "usage: %s ULTRA_HDR.jpg ORDINARY.jpg\n", argv[0]);
    return 2;
  }

  file_bytes_t hdr = {0};
  file_bytes_t ordinary = {0};
  if (!load_file(argv[1], &hdr) || !load_file(argv[2], &ordinary)) {
    fprintf(stderr, "could not load fixture inputs\n");
    free(hdr.data);
    free(ordinary.data);
    return 1;
  }

  if (!is_uhdr_image(hdr.data, (int)hdr.size)) {
    fprintf(stderr, "libultrahdr did not identify the fixture as Ultra HDR\n");
    goto fail;
  }
  if (is_uhdr_image(ordinary.data, (int)ordinary.size)) {
    fprintf(stderr, "libultrahdr incorrectly identified the ordinary JPEG as Ultra HDR\n");
    goto fail;
  }
  if (!contains_bytes(&hdr, "urn:iso:std:iso:ts:21496:-1")) {
    fprintf(stderr, "ISO 21496-1 container marker is missing\n");
    goto fail;
  }

  uhdr_codec_private_t *decoder = uhdr_create_decoder();
  if (decoder == NULL) {
    fprintf(stderr, "could not allocate libultrahdr decoder\n");
    goto fail;
  }
  uhdr_compressed_image_t image = {
      .data = hdr.data,
      .data_sz = hdr.size,
      .capacity = hdr.size,
      .cg = UHDR_CG_UNSPECIFIED,
      .ct = UHDR_CT_UNSPECIFIED,
      .range = UHDR_CR_UNSPECIFIED,
  };
  uhdr_error_info_t result = uhdr_dec_set_image(decoder, &image);
  if (result.error_code != UHDR_CODEC_OK) {
    fprintf(stderr, "libultrahdr rejected input descriptor: %s\n",
            result.has_detail ? result.detail : "no detail");
    uhdr_release_decoder(decoder);
    goto fail;
  }
  result = uhdr_dec_probe(decoder);
  if (result.error_code != UHDR_CODEC_OK) {
    fprintf(stderr, "libultrahdr probe failed: %s\n",
            result.has_detail ? result.detail : "no detail");
    uhdr_release_decoder(decoder);
    goto fail;
  }

  const int base_width = uhdr_dec_get_image_width(decoder);
  const int base_height = uhdr_dec_get_image_height(decoder);
  const int gain_width = uhdr_dec_get_gainmap_width(decoder);
  const int gain_height = uhdr_dec_get_gainmap_height(decoder);
  uhdr_mem_block_t *base = uhdr_dec_get_base_image(decoder);
  uhdr_mem_block_t *gain = uhdr_dec_get_gainmap_image(decoder);
  uhdr_gainmap_metadata_t *metadata = uhdr_dec_get_gainmap_metadata(decoder);

  if (base_width != BASE_WIDTH || base_height != BASE_HEIGHT || gain_width != GAIN_WIDTH ||
      gain_height != GAIN_HEIGHT) {
    fprintf(stderr, "unexpected dimensions: base=%dx%d gainmap=%dx%d\n", base_width, base_height,
            gain_width, gain_height);
    uhdr_release_decoder(decoder);
    goto fail;
  }
  if (base == NULL || gain == NULL || base->data_sz == 0 || gain->data_sz == 0 || metadata == NULL) {
    fprintf(stderr, "container did not expose embedded base, gain map, and metadata\n");
    uhdr_release_decoder(decoder);
    goto fail;
  }
  for (int channel = 0; channel < 3; ++channel) {
    if (!nearly(metadata->max_content_boost[channel], 4.0f, 0.001f) ||
        !nearly(metadata->gamma[channel], 1.0f, 0.001f)) {
      fprintf(stderr, "unexpected channel %d metadata: maxBoost=%g gamma=%g\n", channel,
              metadata->max_content_boost[channel], metadata->gamma[channel]);
      uhdr_release_decoder(decoder);
      goto fail;
    }
  }

  printf("PASS: ISO 21496-1 marker; base=%dx%d (%zu bytes); gainmap=%dx%d (%zu bytes)\n",
         base_width, base_height, base->data_sz, gain_width, gain_height, gain->data_sz);
  printf("PASS: maxContentBoost=%g/%g/%g gamma=%g/%g/%g\n",
         metadata->max_content_boost[0], metadata->max_content_boost[1],
         metadata->max_content_boost[2], metadata->gamma[0], metadata->gamma[1],
         metadata->gamma[2]);
  uhdr_release_decoder(decoder);
  free(hdr.data);
  free(ordinary.data);
  return 0;

fail:
  free(hdr.data);
  free(ordinary.data);
  return 1;
}
