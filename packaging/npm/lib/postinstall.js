#!/usr/bin/env node

'use strict';

const fs = require('fs');
const https = require('https');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const pkg = require('../package.json');

function resolveGOOS(platform) {
  if (platform === 'darwin') {
    return 'darwin';
  }
  if (platform === 'linux') {
    return 'linux';
  }
  throw new Error(`unsupported platform: ${platform}`);
}

function resolveGOARCH(arch) {
  if (arch === 'x64') {
    return 'amd64';
  }
  if (arch === 'arm64') {
    return 'arm64';
  }
  throw new Error(`unsupported architecture: ${arch}`);
}

function releaseVersion() {
  const override = process.env.MCPX_GO_BINARY_VERSION;
  if (override && override.trim() !== '') {
    return override.trim();
  }
  return pkg.version;
}

function releaseAssetURL(version, goos, goarch) {
  const baseURL = (process.env.MCPX_GO_RELEASE_BASE_URL || 'https://github.com/lydakis/mcpx/releases/download').replace(/\/+$/, '');
  const tagPrefix = process.env.MCPX_GO_RELEASE_TAG_PREFIX || 'v';
  const tag = `${tagPrefix}${version}`;
  const asset = `mcpx_${version}_${goos}_${goarch}.tar.gz`;
  return `${baseURL}/${tag}/${asset}`;
}

function download(url, destination, redirects) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        if (redirects >= 5) {
          res.resume();
          reject(new Error(`too many redirects while downloading ${url}`));
          return;
        }

        const nextURL = new URL(res.headers.location, url).toString();
        res.resume();
        resolve(download(nextURL, destination, redirects + 1));
        return;
      }

      if (res.statusCode !== 200) {
        let body = '';
        res.setEncoding('utf8');
        res.on('data', (chunk) => {
          if (body.length < 512) {
            body += chunk;
          }
        });
        res.on('end', () => {
          reject(new Error(`download failed (${res.statusCode}) for ${url}${body ? `: ${body}` : ''}`));
        });
        return;
      }

      const out = fs.createWriteStream(destination, { mode: 0o600 });
      res.pipe(out);
      out.on('finish', () => {
        out.close(resolve);
      });
      out.on('error', (error) => {
        out.close(() => {
          fs.unlink(destination, () => reject(error));
        });
      });
    });

    req.on('error', (error) => {
      reject(error);
    });
  });
}

async function install() {
  if (process.env.MCPX_GO_SKIP_DOWNLOAD === '1') {
    console.log('mcpx-go: skipping bundled binary download (MCPX_GO_SKIP_DOWNLOAD=1).');
    return;
  }

  const goos = resolveGOOS(process.platform);
  const goarch = resolveGOARCH(process.arch);
  const version = releaseVersion();

  const vendorDir = path.join(__dirname, '..', 'vendor');
  const binaryPath = path.join(vendorDir, 'mcpx');

  if (fs.existsSync(binaryPath)) {
    return;
  }

  fs.mkdirSync(vendorDir, { recursive: true });

  const url = releaseAssetURL(version, goos, goarch);
  const archivePath = path.join(os.tmpdir(), `mcpx-go-${version}-${process.pid}-${Date.now()}.tar.gz`);

  console.log(`mcpx-go: downloading ${url}`);
  await download(url, archivePath, 0);

  try {
    const extract = spawnSync('tar', ['-xzf', archivePath, '-C', vendorDir], { stdio: 'inherit' });
    if (extract.error) {
      throw new Error(`failed to run tar: ${extract.error.message}`);
    }
    if (extract.status !== 0) {
      throw new Error(`tar exited with status ${extract.status}`);
    }
    if (!fs.existsSync(binaryPath)) {
      throw new Error('archive extraction did not produce mcpx binary');
    }
    fs.chmodSync(binaryPath, 0o755);
  } finally {
    fs.unlink(archivePath, () => {});
  }
}

install().catch((error) => {
  console.error(`mcpx-go: ${error.message}`);
  process.exit(1);
});
