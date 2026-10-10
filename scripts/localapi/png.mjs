// 纯 Node 的 PNG 合成：本地模式没有图片渠道时，用它生成一张可复现的占位图，
// 让"生图 → 落盘 → 历史/下载"的完整链路在零配置下也能跑通。
// 只依赖 node:zlib，不引入 canvas 之类的原生依赖。
import zlib from 'node:zlib';

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let index = 0; index < 256; index += 1) {
    let value = index;
    for (let bit = 0; bit < 8; bit += 1) {
      value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    }
    table[index] = value >>> 0;
  }
  return table;
})();

function crc32(buffer) {
  let crc = 0xffffffff;
  for (let index = 0; index < buffer.length; index += 1) {
    crc = CRC_TABLE[(crc ^ buffer[index]) & 0xff] ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length, 0);
  const typeAndData = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(typeAndData), 0);
  return Buffer.concat([length, typeAndData, crc]);
}

function encodePNG(width, height, rgba) {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8; // bit depth
  header[9] = 6; // color type: RGBA
  header[10] = 0; // deflate
  header[11] = 0; // adaptive filtering
  header[12] = 0; // no interlace

  const stride = width * 4;
  const raw = Buffer.alloc((stride + 1) * height);
  for (let y = 0; y < height; y += 1) {
    raw[y * (stride + 1)] = 0;
    rgba.copy(raw, y * (stride + 1) + 1, y * stride, (y + 1) * stride);
  }

  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header),
    chunk('IDAT', zlib.deflateSync(raw, {level: 6})),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

export function hashSeed(value) {
  let hash = 0x811c9dc5;
  const text = String(value ?? '');
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash >>> 0;
}

export function parseSize(sizeCode, fallback = [1024, 1024]) {
  const match = /^(\d{2,5})x(\d{2,5})$/.exec(String(sizeCode || '').trim());
  if (!match) return fallback;
  const width = Math.min(2048, Math.max(256, Number.parseInt(match[1], 10)));
  const height = Math.min(2048, Math.max(256, Number.parseInt(match[2], 10)));
  return [width, height];
}

function channel(seed, shift, base, spread) {
  return Math.min(255, base + ((seed >>> shift) % spread));
}

/**
 * 依据 prompt 生成一张确定性的占位图：渐变底色 + 对角光带 + 居中矩形，
 * 同一个 prompt 永远得到同一张图（与 Go 端 mock provider 的行为一致）。
 */
export function placeholderPNG(prompt, sizeCode = '1024x1024') {
  const [width, height] = parseSize(sizeCode);
  const seed = hashSeed(prompt);
  const rgba = Buffer.alloc(width * height * 4);

  const top = [channel(seed, 0, 18, 70), channel(seed, 8, 26, 70), channel(seed, 16, 55, 90)];
  const bottom = [channel(seed, 24, 8, 40), channel(seed, 4, 10, 40), channel(seed, 12, 24, 60)];
  const accent = [channel(seed, 20, 90, 150), channel(seed, 2, 120, 130), 235];

  const bandPeriod = 20 + (seed % 24);
  const motif = {
    left: Math.round(width * 0.12),
    top: Math.round(height * 0.12),
    right: Math.round(width * 0.88),
    bottom: Math.round(height * 0.88),
  };
  const inner = {
    left: motif.left + Math.round((motif.right - motif.left) * 0.18),
    top: motif.top + Math.round((motif.bottom - motif.top) * 0.18),
    right: motif.right - Math.round((motif.right - motif.left) * 0.18),
    bottom: motif.bottom - Math.round((motif.bottom - motif.top) * 0.18),
  };
  const centerX = width / 2;
  const centerY = height / 2;
  const radius = Math.min(width, height) * 0.34;

  for (let y = 0; y < height; y += 1) {
    const verticalRatio = y / Math.max(1, height - 1);
    for (let x = 0; x < width; x += 1) {
      const offset = (y * width + x) * 4;
      let red = Math.round(top[0] + (bottom[0] - top[0]) * verticalRatio);
      let green = Math.round(top[1] + (bottom[1] - top[1]) * verticalRatio);
      let blue = Math.round(top[2] + (bottom[2] - top[2]) * verticalRatio);

      if ((x + y) % bandPeriod === 0) {
        red = Math.min(255, red + 26);
        green = Math.min(255, green + 26);
        blue = Math.min(255, blue + 26);
      }

      const insideMotif = x >= motif.left && x < motif.right && y >= motif.top && y < motif.bottom;
      if (insideMotif) {
        const insideInner = x >= inner.left && x < inner.right && y >= inner.top && y < inner.bottom;
        if (!insideInner) {
          red = Math.round((red + accent[0]) / 2);
          green = Math.round((green + accent[1]) / 2);
          blue = Math.round((blue + accent[2]) / 2);
        }
      }

      // 中心柔光，让占位图不至于是一片纯色块。
      const distance = Math.hypot(x - centerX, y - centerY);
      if (distance < radius) {
        const strength = 1 - distance / radius;
        red = Math.min(255, Math.round(red + accent[0] * 0.35 * strength));
        green = Math.min(255, Math.round(green + accent[1] * 0.35 * strength));
        blue = Math.min(255, Math.round(blue + accent[2] * 0.35 * strength));
      }

      rgba[offset] = red;
      rgba[offset + 1] = green;
      rgba[offset + 2] = blue;
      rgba[offset + 3] = 255;
    }
  }

  return encodePNG(width, height, rgba);
}

export {encodePNG};
