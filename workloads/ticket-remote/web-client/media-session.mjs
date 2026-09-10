const MAX_PICTURE_BYTES = 2 * 1024 * 1024;
export const MAX_PICTURE_AGE_MS = 3000;

export function parsePicture(raw) {
  if (!(raw instanceof ArrayBuffer) || raw.byteLength <= 93 || raw.byteLength > MAX_PICTURE_BYTES + 93) return null;
  const view = new DataView(raw);
  if (view.getUint32(0) !== 0x54534633 || view.getUint8(4) !== 1) return null;
  const values = [5, 13, 21, 29, 37, 45, 53, 61, 69, 77, 85].map((offset) => {
    const value = Number(view.getBigUint64(offset));
    return Number.isSafeInteger(value) ? value : NaN;
  });
  if (!values.every(Number.isFinite)) return null;
  const [epoch, sequence, attempt, codecGeneration, captureStart, captureComplete,
    codecInput, codecOutput, emission, calibrationGeneration, uncertainty] = values;
  if (!epoch || !sequence || !captureStart) return null;
  return { epoch, sequence, attempt, codecGeneration, captureStart, captureComplete,
    codecInput, codecOutput, emission, calibrationGeneration, uncertainty,
    timestamp: captureStart, data: new Uint8Array(raw, 93) };
}

function nalUnits(data) {
  const starts = [];
  for (let i = 0; i + 3 < data.length; i++) {
    if (data[i] !== 0 || data[i + 1] !== 0) continue;
    const length = data[i + 2] === 1 ? 3 : data[i + 2] === 0 && data[i + 3] === 1 ? 4 : 0;
    if (length) { starts.push([i, length]); i += length - 1; }
  }
  return starts.map(([offset, length], i) => data.subarray(offset + length, starts[i + 1]?.[0] ?? data.length))
    .filter((unit) => unit.length);
}

function avcDescription(sps, pps) {
  if (!sps || sps.length < 4 || !pps || sps.length > 65535 || pps.length > 65535) return null;
  const bytes = new Uint8Array(11 + sps.length + pps.length);
  bytes.set([1, sps[1], sps[2], sps[3], 255, 225, sps.length >> 8, sps.length & 255]);
  bytes.set(sps, 8);
  bytes.set([1, pps.length >> 8, pps.length & 255], 8 + sps.length);
  bytes.set(pps, 11 + sps.length);
  return bytes;
}

function avcSample(units) {
  const bytes = new Uint8Array(units.reduce((n, unit) => n + 4 + unit.length, 0));
  const view = new DataView(bytes.buffer);
  let offset = 0;
  for (const unit of units) {
    view.setUint32(offset, unit.length);
    bytes.set(unit, offset + 4);
    offset += unit.length + 4;
  }
  return bytes;
}

// One socket, one decoder, one picture being decoded, and one newest waiting
// picture. Recovery policy belongs to the page, never to transport callbacks.
export class MediaSession {
  constructor(config, handlers) {
    this.page = config;
    this.handlers = handlers;
    this.generation = 0;
    this.socket = null;
    this.decoder = null;
    this.config = null;
    this.waiting = null;
    this.decoding = null;
    this.rendered = null;
    this.clock = null;
    this.probe = null;
    this.nextProbeAt = 0;
    this.received = 0;
    this.decoded = 0;
    this.presented = 0;
    this.lastPacketAt = 0;
    this.startedAt = 0;
    this.failed = false;
  }

  open(early = null, ownerGeneration = 0) {
    this.close();
    this.ownerGeneration = ownerGeneration;
    this.failed = false;
    this.startedAt = performance.now();
    this.lastPacketAt = this.startedAt;
    const generation = this.generation;
    const url = new URL('/api/v1/stream', location.href);
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    url.searchParams.set('page_version', this.page.pageVersion);
    url.searchParams.set('asset_version', this.page.assetVersion);
    url.searchParams.set('visibility', document.visibilityState);
    const protocols = ['ticket.video.v1'];
    if (/^ticket\.startup\.[0-9a-f]{32}$/.test(this.page.startupRunOrigin || '')) protocols.push(this.page.startupRunOrigin);
    const socket = early?.socket && early.socket.readyState < 2 ? early.socket : new WebSocket(url, protocols);
    this.socket = socket;
    socket.binaryType = 'arraybuffer';
    socket.onmessage = (event) => { if (generation === this.generation) this.receive(event.data); };
    socket.onopen = () => this.handlers.onStatus?.('connected');
    socket.onerror = () => { if (generation === this.generation) this.fail('video_connection_failed'); };
    socket.onclose = () => { if (generation === this.generation) this.fail('video_connection_closed'); };
    if (early?.opening) this.receive(early.opening);
    if (early?.config) this.receive(early.config);
    if (early?.frame) this.receive(early.frame);
  }

  close() {
    this.generation++;
    if (this.socket) {
      this.socket.onclose = this.socket.onerror = this.socket.onmessage = null;
      this.socket.close();
    }
    this.socket = null;
    this.closeDecoder();
    this.config = this.clock = this.probe = this.waiting = this.rendered = null;
    this.received = this.decoded = this.presented = 0;
  }

  closeDecoder() {
    if (this.decoder && this.decoder.state !== 'closed') this.decoder.close();
    this.decoder = null;
    this.decoding = null;
    this.sps = this.pps = null;
  }

  send(message) {
    if (this.socket?.readyState !== WebSocket.OPEN) return false;
    try { this.socket.send(JSON.stringify(message)); return true; } catch { return false; }
  }

  fail(reason) {
    if (this.failed) return;
    this.failed = true;
    this.handlers.onFailure?.(reason, this.ownerGeneration);
  }

  receive(raw) {
    if (typeof raw === 'string') {
      if (raw.length > 65536) return this.fail('invalid_video_config');
      let message;
      try { message = JSON.parse(raw); } catch { return this.fail('invalid_video_config'); }
      if (message.serverVersion && message.serverVersion !== this.page.pageVersion) {
        this.handlers.onVersion?.(message.serverVersion);
      }
      if (message.type === 'clock_probe_result') return this.acceptClock(message);
      if (message.type === 'opening' && ['warm','cold','recovery'].includes(message.openingClass)) {
        this.handlers.onOpening?.(message.openingClass);
        return;
      }
      if (message.type !== 'config') return;
      if (message.frameEnvelope !== 'tsf3' || message.feedbackVersion !== 2 ||
        message.frameDependencyMode !== 'all_intra' || Number(message.fps) !== 1 ||
        Number(message.sourceFps) !== 1 || Number(message.keyframeIntervalFrames) !== 1 ||
        !Number.isSafeInteger(message.feedbackConfigGeneration) || message.feedbackConfigGeneration <= 0 ||
        !Number.isInteger(message.width) || !Number.isInteger(message.height) ||
        message.width <= 0 || message.height <= 0 || message.width * message.height > 16777216) {
        return this.fail('unsupported_video_config');
      }
      this.closeDecoder();
      this.config = message;
      this.epoch = Number(message.streamEpoch || 0);
      this.waiting = this.rendered = this.clock = this.probe = null;
      this.received = this.decoded = this.presented = 0;
      this.nextProbeAt = 0;
      this.feedback();
      this.tick();
      this.handlers.onConfig?.(message);
      return;
    }
    const picture = parsePicture(raw);
    if (!picture || !this.config || (this.epoch && picture.epoch !== this.epoch) || picture.sequence <= this.received) return;
    this.epoch = picture.epoch;
    this.received = picture.sequence;
    this.lastPacketAt = performance.now();
    picture.configGeneration = this.config.feedbackConfigGeneration;
    picture.sessionGeneration = this.generation;
    // Receipt is independent of freshness and local decode success.
    this.feedback();
    this.waiting = picture;
    this.decodeNewest();
  }

  age(picture, now = performance.now()) {
    if (!picture || picture.sessionGeneration !== this.generation || picture.epoch !== this.epoch ||
      picture.configGeneration !== this.config?.feedbackConfigGeneration || !this.clock ||
      now < this.clock.at || now - this.clock.at > 15000) return Infinity;
    return (Math.max(0, this.clock.upper + (now - this.clock.at) * 1000 - picture.captureStart) + picture.uncertainty) / 1000;
  }

  acceptClock(message) {
    const now = performance.now();
    const pending = this.probe;
    if (!pending || message.version !== 1 || message.probeId !== pending.id ||
      message.configGeneration !== this.config?.feedbackConfigGeneration ||
      message.clientSendUnixMicros !== pending.wall) return;
    const receive = Number(message.serverReceiveUnixMicros), send = Number(message.serverSendUnixMicros);
    const elapsed = (now - pending.at) * 1000;
    if (![receive, send].every((n) => Number.isSafeInteger(n) && n > 0) ||
      elapsed < 0 || send < receive || send - receive > elapsed) return;
    this.clock = { at: now, upper: receive + elapsed };
    this.probe = null;
    this.nextProbeAt = now + 5000;
    this.decodeNewest();
  }

  decodeNewest() {
    if (!this.waiting || this.decoding || this.failed) return;
    if (!globalThis.VideoDecoder || !globalThis.EncodedVideoChunk) return this.fail('video_decoder_unavailable');
    const picture = this.waiting;
    // The bounded clock reply may arrive after the first picture.
    if (!this.clock) return;
    this.waiting = null;
    if (this.age(picture) > MAX_PICTURE_AGE_MS) return;
    const units = nalUnits(picture.data);
    for (const unit of units) {
      if ((unit[0] & 31) === 7) this.sps = unit;
      if ((unit[0] & 31) === 8) this.pps = unit;
    }
    if (!units.some((unit) => (unit[0] & 31) === 5)) return this.fail('independent_picture_required');
    const description = avcDescription(this.sps, this.pps);
    if (!description) return this.fail('video_parameter_sets_missing');
    if (!this.decoder) {
      const generation = this.generation;
      const decoder = new VideoDecoder({
        output: (frame) => {
          try {
            if (generation !== this.generation || decoder !== this.decoder) return;
            const metadata = this.decoding;
            this.decoding = null;
            if (!metadata || frame.timestamp !== metadata.timestamp) return this.fail('decoded_picture_identity_mismatch');
            this.decoded = metadata.sequence;
            const age = this.age(metadata);
            if (age <= MAX_PICTURE_AGE_MS) {
              this.handlers.onFrame(frame, { ...metadata, visualAgeMillis: age,
                visualAgeKnown: true, visualAgeConservative: true, renderedAt: performance.now() });
            }
            this.feedback();
            this.decodeNewest();
          } catch { this.fail('picture_presentation_failed'); }
          finally { frame.close(); }
        },
        error: () => { if (generation === this.generation) this.fail('video_decoder_failed'); }
      });
      this.decoder = decoder;
      const codec = `avc1.${[this.sps[1], this.sps[2], this.sps[3]].map((n) => n.toString(16).padStart(2, '0')).join('')}`;
      try {
        decoder.configure({ codec, codedWidth: this.config.width, codedHeight: this.config.height,
          description, optimizeForLatency: true, hardwareAcceleration: 'prefer-hardware' });
      } catch { return this.fail('video_decoder_configuration_failed'); }
    }
    this.decoding = picture;
    this.decodeStartedAt = performance.now();
    try { this.decoder.decode(new EncodedVideoChunk({ type: 'key', timestamp: picture.timestamp, data: avcSample(units) })); }
    catch { this.fail('video_decode_failed'); }
  }

  noteRendered(metadata, presented = false) {
    if (metadata.sessionGeneration !== this.generation || metadata.configGeneration !== this.config?.feedbackConfigGeneration || metadata.epoch !== this.epoch) return;
    this.rendered = metadata;
    if (presented) this.presented = metadata.sequence;
    this.feedback();
  }

  feedback() {
    if (!this.config) return;
    const age = this.age(this.rendered);
    const rendered = Math.min(this.decoded, this.rendered?.sequence || 0);
    this.send({ type: 'stream_feedback', version: 2, epoch: this.epoch,
      configGeneration: this.config.feedbackConfigGeneration, receivedSequence: this.received,
      decodedSequence: this.decoded, renderedSequence: rendered, presentedSequence: Math.min(rendered, this.presented),
      renderedKeyframeSequence: rendered, decoderQueueSize: this.decoder?.decodeQueueSize || 0,
      ageKnown: Number.isFinite(age), renderedVisualAgeMillis: Number.isFinite(age) ? Math.min(60000, Math.round(age)) : 0,
      visibility: document.visibilityState === 'hidden' ? 'hidden' : 'visible' });
  }

  tick() {
    if (!this.socket || this.failed) return;
    const now = performance.now();
    if (this.config && now >= this.nextProbeAt) {
      const id = crypto.randomUUID(), wall = Date.now() * 1000;
      if (this.send({ type: 'clock_probe', version: 1, probeId: id,
        configGeneration: this.config.feedbackConfigGeneration, clientSendUnixMicros: wall })) {
        this.probe = { id, at: now, wall };
        this.nextProbeAt = now + 1000;
      }
    }
    if ((this.decoding && now - this.decodeStartedAt > 3000) || now - this.lastPacketAt > 10000) this.fail('video_progress_timeout');
    this.feedback();
  }
}
