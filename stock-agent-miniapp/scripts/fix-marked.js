/**
 * 修复 TDesign 内嵌的 marked 副本（PC 端微信兼容性）
 *
 * 背景：tdesign-miniprogram 的发布包自带 miniprogram_npm/marked，
 * 该版本含大量 ES2020+ 语法（可选链 ?.、空值合并 ??、Array.at）。
 * PC 端微信的小程序运行时不支持这些语法，会导致：
 *   SyntaxError: Invalid or unexpected token
 *   → module 'app.js' is not defined → 白屏
 *
 * 本脚本用项目根 miniprogram_npm/marked（marked@4.0.0，语法干净）覆盖它。
 *
 * 使用时机：每次执行「构建 npm」或 `cli build-npm` 之后运行一次：
 *   npm run fix:npm
 */
const fs = require('fs');
const path = require('path');

const rootMarked = path.join(__dirname, '..', 'miniprogram_npm', 'marked', 'index.js');
const innerMarkedDir = path.join(
  __dirname,
  '..',
  'miniprogram_npm',
  'tdesign-miniprogram',
  'miniprogram_npm',
  'marked'
);
const innerMarked = path.join(innerMarkedDir, 'index.js');

if (!fs.existsSync(rootMarked)) {
  console.error('未找到根级 marked 产物：', rootMarked);
  console.error('请先执行「构建 npm」。');
  process.exit(1);
}

fs.mkdirSync(innerMarkedDir, { recursive: true });
fs.copyFileSync(rootMarked, innerMarked);

const size = Math.round(fs.statSync(innerMarked).size / 1024);
console.log(`已用 marked@4.0.0（${size}KB，ES2017 语法）覆盖 TDesign 内嵌副本。`);
