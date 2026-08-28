async function defaultWaitForReady(image, requestAnimationFrame) {
  if (!image || typeof image.decode !== 'function') {
    throw new Error('HDR image decode unavailable');
  }
  await image.decode();
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await new Promise((resolve) => requestAnimationFrame(resolve));
}

function setImageVisible(image, visible) {
  image.hidden = !visible;
  if (typeof image.setAttribute === 'function') {
    image.setAttribute('aria-hidden', visible ? 'false' : 'true');
  }
  if (image.style) {
    image.style.zIndex = visible ? '3' : '2';
    image.style.opacity = '1';
  }
}

function stageImageBehind(image) {
  image.hidden = false;
  if (typeof image.setAttribute === 'function') {
    image.setAttribute('aria-hidden', 'true');
  }
  if (image.style) {
    image.style.zIndex = '2';
    image.style.opacity = '1';
  }
}

function removeImageSource(image) {
  if (typeof image.removeAttribute === 'function') {
    image.removeAttribute('src');
  } else {
    image.src = '';
  }
}

export function advanceExperimentalHDRReplacementFailure(previousFailures, status, hasActive, limit = 3) {
  const failures = Math.max(0, Math.trunc(Number(previousFailures) || 0));
  if (status === 'committed') return { failures: 0, fallback: false };
  if (status !== 'failed') return { failures, fallback: false };
  const nextFailures = failures + 1;
  return {
    failures: nextFailures,
    fallback: !hasActive || nextFailures >= Math.max(1, Math.trunc(Number(limit) || 0))
  };
}

export class ExperimentalHDRImageSwitcher {
  constructor(images, options = {}) {
    if (!Array.isArray(images) || images.length !== 2 || !images[0] || !images[1] || images[0] === images[1]) {
      throw new TypeError('Experimental HDR switching requires two distinct image surfaces');
    }

    this.images = images.slice();
    this.createObjectURL = options.createObjectURL || ((blob) => URL.createObjectURL(blob));
    this.revokeObjectURL = options.revokeObjectURL || ((url) => URL.revokeObjectURL(url));
    this.requestAnimationFrame = options.requestAnimationFrame || globalThis.requestAnimationFrame?.bind(globalThis) ||
      ((callback) => setTimeout(callback, 0));
    this.waitForReady = options.waitForReady || ((image) => defaultWaitForReady(image, this.requestAnimationFrame));
    this.afterSwap = options.afterSwap || (() => {});

    this.generation = 0;
    this.active = null;
    this.candidate = null;
    this.liveURLs = new Set();

    for (const image of this.images) {
      setImageVisible(image, false);
    }
  }

  hasActive() {
    return this.active !== null;
  }

  setDimensions(width, height) {
    const normalizedWidth = Math.max(0, Math.trunc(Number(width) || 0));
    const normalizedHeight = Math.max(0, Math.trunc(Number(height) || 0));
    for (const image of this.images) {
      image.width = normalizedWidth;
      image.height = normalizedHeight;
    }
  }

  async present(blob, options = {}) {
    const isCurrent = typeof options.isCurrent === 'function' ? options.isCurrent : () => true;
    if (!isCurrent()) return { status: 'stale' };
    const generation = ++this.generation;

    this.releaseCandidate();
    const image = this.images.find((surface) => !this.active || surface !== this.active.image);
    let candidate;

    try {
      const url = this.createObjectURL(blob);
      candidate = { generation, image, url };
      this.liveURLs.add(url);
      this.candidate = candidate;
      if (this.active) {
        setImageVisible(this.active.image, true);
        stageImageBehind(image);
      } else {
        setImageVisible(image, false);
      }
      image.src = url;
      await this.waitForReady(image, url);
    } catch (error) {
      if (generation !== this.generation || (candidate && this.candidate !== candidate)) {
        return { status: 'stale' };
      }
      if (candidate) this.releaseCandidate(candidate);
      if (this.active) return { status: 'failed', error };
      throw error;
    }

    if (this.candidate !== candidate || generation !== this.generation || !isCurrent()) {
      this.releaseCandidate(candidate);
      return { status: 'stale' };
    }

    const previous = this.active;
    setImageVisible(candidate.image, true);
    if (previous) setImageVisible(previous.image, false);
    this.active = candidate;
    this.candidate = null;

    try {
      this.afterSwap({
        image: candidate.image,
        url: candidate.url,
        previousImage: previous && previous.image,
        previousURL: previous && previous.url
      });
    } finally {
      if (previous) this.release(previous);
    }

    return { status: 'committed' };
  }

  clear() {
    ++this.generation;
    const candidate = this.candidate;
    const active = this.active;
    this.candidate = null;
    this.active = null;
    if (candidate) this.release(candidate);
    if (active) this.release(active);
    for (const image of this.images) {
      setImageVisible(image, false);
      removeImageSource(image);
    }
  }

  releaseCandidate(candidate = this.candidate) {
    if (!candidate || this.candidate !== candidate) return;
    this.candidate = null;
    this.release(candidate);
  }

  release(entry) {
    setImageVisible(entry.image, false);
    removeImageSource(entry.image);
    if (!this.liveURLs.delete(entry.url)) return;
    this.revokeObjectURL(entry.url);
  }
}
