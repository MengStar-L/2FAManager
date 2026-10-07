// Package the generated mascot at native Windows icon sizes without altering the artwork.
import { chromium } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const source = await readFile(process.argv[2] || new URL('../../build/appicon-source.png', import.meta.url));
const browser = await chromium.launch({ channel: 'msedge', headless: true });
try {
  const page = await browser.newPage();
  const sizes = [16, 20, 24, 32, 40, 48, 64, 128, 256, 512];
  const result = await page.evaluate(async ({ data, sizes }) => {
    const image = new Image();
    image.src = data;
    await image.decode();
    const probe = document.createElement('canvas');
    probe.width = image.width; probe.height = image.height;
    const context = probe.getContext('2d');
    context.drawImage(image, 0, 0);
    const pixels = context.getImageData(0, 0, image.width, image.height).data;
    let transparent = 0, opaque = 0;
    for (let i = 3; i < pixels.length; i += 4) {
      if (pixels[i] === 0) transparent++;
      if (pixels[i] === 255) opaque++;
    }
    if (!transparent || !opaque) throw new Error('The app icon must contain both transparent and opaque pixels');
    const images = sizes.map(size => {
      const canvas = document.createElement('canvas');
      canvas.width = canvas.height = size;
      const ctx = canvas.getContext('2d');
      ctx.imageSmoothingEnabled = true;
      ctx.imageSmoothingQuality = 'high';
      const scale = size / Math.max(image.width, image.height);
      const width = image.width * scale, height = image.height * scale;
      ctx.drawImage(image, (size - width) / 2, (size - height) / 2, width, height);
      return canvas.toDataURL('image/png').split(',')[1];
    });
    return { images, width: image.width, height: image.height, transparent, opaque };
  }, { data: `data:image/png;base64,${source.toString('base64')}`, sizes });
  const pngs = result.images.map(data => Buffer.from(data, 'base64'));
  const entries = sizes.filter(size => size <= 256);
  const header = Buffer.alloc(6 + 16 * entries.length);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(entries.length, 4);
  let offset = header.length;
  for (let i = 0; i < entries.length; i++) {
    const start = 6 + 16 * i;
    header[start] = header[start + 1] = entries[i] === 256 ? 0 : entries[i];
    header.writeUInt16LE(1, start + 4);
    header.writeUInt16LE(32, start + 6);
    header.writeUInt32LE(pngs[i].length, start + 8);
    header.writeUInt32LE(offset, start + 12);
    offset += pngs[i].length;
  }
  await mkdir(new URL('../src/assets/images/', import.meta.url), { recursive: true });
  await writeFile(new URL('../../build/appicon.png', import.meta.url), pngs.at(-1));
  await writeFile(new URL('../src/assets/images/app-icon.png', import.meta.url), pngs[sizes.indexOf(128)]);
  await writeFile(new URL('../../build/windows/icon.ico', import.meta.url), Buffer.concat([header, ...pngs.slice(0, entries.length)]));
  console.log(JSON.stringify({ sourceSize: [result.width, result.height], transparentPixels: result.transparent, sizes: entries, icon: fileURLToPath(new URL('../../build/windows/icon.ico', import.meta.url)) }));
} finally { await browser.close(); }
