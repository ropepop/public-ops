# Synthetic Ultra HDR lab

This isolated lab generates a synthetic ISO 21496-1 Ultra HDR JPEG. It never reads Ticket inputs and contains no Ticket or user pixels.

The paired inputs describe the same BT.709 image as:

- an 8-bit sRGB SDR intent;
- a linear RGBA half-float HDR intent whose RGB values are the exact sRGB-linear values multiplied by 4.0.

`ultrahdr_app` 2.0.2 computes and embeds the gain map. The HTML deliberately renders the result in direct `<img>` elements because a normal 2D canvas is not evidence of an HDR-preserving path. One image requests `dynamic-range-limit: standard`; the other requests `dynamic-range-limit: no-limit`.

Generation also copies this same sanitized artifact to `internal/web/static/hdr-capability-fixture.jpg`. The authenticated member page must decode it successfully before it exposes the live HDR toggle.

Generate and validate:

```bash
./generate_fixture.sh
```

Validate already-generated artifacts:

```bash
./validate_fixture.sh
```

Serve the directory over HTTP for browser testing, for example:

```bash
ruby -run -e httpd . -p 8097 -b 127.0.0.1
```

The local HTTP server is only a lab convenience. The validator is the structural gate: it requires libultrahdr to report gain-map metadata, requires the same probe to reject the ordinary JPEG control, checks the ISO 21496-1 marker, verifies a 768×512 base plus a non-empty 384×256 embedded gain map, requires a three-channel maximum content boost of 4.0, and decodes a 768×512 linear half-float HDR result. The manifest records generation timings and hashes.

Passing those checks proves the file is a structurally valid, decodable gain-map image with a 4× source intent. It does not prove that a particular Safari/iOS version promotes the image to EDR, that a Home Screen app preserves EDR, or that the panel reached any measured luminance. Physical-device observation or measurement remains a separate gate, and web content cannot move the iPhone brightness slider.

## Primary-source platform basis

- [WebKit: Safari 26 HDR images](https://webkit.org/blog/17333/webkit-features-in-safari-26-0/#hdr-images) documents HDR images on iOS and `dynamic-range-limit`.
- [Apple WWDC25: Develop for HDR images](https://developer.apple.com/videos/play/wwdc2025/233/) describes direct HDR image rendering, SDR fallback, and author dynamic-range limits.
- [W3C CSS Color HDR](https://www.w3.org/TR/css-color-hdr-1/#dynamic-range-limit) defines `no-limit` without exposing exact display headroom.
- [W3C Media Queries 5](https://www.w3.org/TR/mediaqueries-5/#dynamic-range) defines `dynamic-range: high` as capability, not proof that HDR mode is active.
- [Google libultrahdr](https://github.com/google/libultrahdr) is the reference gain-map encoder/decoder used by this fixture and the isolated live candidate.

The browser gate combines the media query, CSS support, and successful decode of this known fixture. Those checks establish eligibility only. A luminance meter on the exact installed iPhone Home Screen app remains necessary to prove emitted EDR light output. Even then, web content cannot set the iPhone brightness control or guarantee absolute maximum panel luminance.
