// 生成助手头像 logo：蓝色圆角方块 + 白色 K 线上升趋势（纯 Node，无依赖）
// 运行：node scripts/gen-logo.js  → 输出 assets/logo.png
const fs = require('fs');
const path = require('path');
const zlib = require('zlib');

const S = 144;          // 画布边长
const R = 32;           // 圆角半径
const BG = [37, 89, 235, 255];   // #2559EB
const FG = [255, 255, 255, 255]; // 白色

// 折线顶点（K 线上升趋势）
const PTS = [
  [30, 100],
  [54, 74],
  [76, 86],
  [102, 52],
  [114, 42]
];
const THICK = 7;

function inRoundRect(x, y) {
  if (x < 0 || y < 0 || x >= S || y >= S) return false;
  const nx = Math.min(Math.max(x, R), S - 1 - R);
  const ny = Math.min(Math.max(y, R), S - 1 - R);
  const dx = x - nx, dy = y - ny;
  return dx * dx + dy * dy <= R * R;
}

function distToSeg(px, py, [ax, ay], [bx, by]) {
  const vx = bx - ax, vy = by - ay;
  const wx = px - ax, wy = py - ay;
  const len2 = vx * vx + vy * vy;
  let t = len2 ? (wx * vx + wy * vy) / len2 : 0;
  t = Math.max(0, Math.min(1, t));
  const dx = px - (ax + t * vx), dy = py - (ay + t * vy);
  return Math.sqrt(dx * dx + dy * dy);
}

// 像素填充
const buf = Buffer.alloc(S * S * 4);
for (let y = 0; y < S; y++) {
  for (let x = 0; x < S; x++) {
    let c = [0, 0, 0, 0];
    if (inRoundRect(x, y)) c = BG;
    for (let i = 0; i < PTS.length - 1; i++) {
      if (distToSeg(x + 0.5, y + 0.5, PTS[i], PTS[i + 1]) <= THICK / 2) {
        c = FG;
        break;
      }
    }
    // 终点小圆点
    const [ex, ey] = PTS[PTS.length - 1];
    const dd = (x + 0.5 - ex) ** 2 + (y + 0.5 - ey) ** 2;
    if (dd <= 10 * 10) c = FG;
    const o = (y * S + x) * 4;
    buf[o] = c[0]; buf[o + 1] = c[1]; buf[o + 2] = c[2]; buf[o + 3] = c[3];
  }
}

// PNG 编码
const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();
function crc32(b) {
  let c = 0xffffffff;
  for (let i = 0; i < b.length; i++) c = CRC_TABLE[(c ^ b[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}
function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}

const ihdr = Buffer.alloc(13);
ihdr.writeUInt32BE(S, 0);
ihdr.writeUInt32BE(S, 4);
ihdr[8] = 8;  // bit depth
ihdr[9] = 6;  // RGBA
const raw = Buffer.alloc(S * (S * 4 + 1));
for (let y = 0; y < S; y++) {
  raw[y * (S * 4 + 1)] = 0; // filter: none
  buf.copy(raw, y * (S * 4 + 1) + 1, y * S * 4, (y + 1) * S * 4);
}
const png = Buffer.concat([
  Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
  chunk('IHDR', ihdr),
  chunk('IDAT', zlib.deflateSync(raw, { level: 9 })),
  chunk('IEND', Buffer.alloc(0))
]);

const out = path.join(__dirname, '..', 'assets', 'logo.png');
fs.mkdirSync(path.dirname(out), { recursive: true });
fs.writeFileSync(out, png);
console.log('已生成', out, Math.round(png.length / 1024) + 'KB');
