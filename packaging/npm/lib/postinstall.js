#!/usr/bin/env node

'use strict';

const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const https = require('https');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const pkg = require('../package.json');
let checksumManifest = null;
try {
  // Filled during release publish; provides expected SHA-256 per archive.
  checksumManifest = require('./checksums.json');
} catch (_error) {
  checksumManifest = null;
}

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

function releaseAssetName(version, goos, goarch) {
  return `mcpx_${version}_${goos}_${goarch}.tar.gz`;
}

function releaseBaseURL() {
  return (process.env.MCPX_GO_RELEASE_BASE_URL || 'https://github.com/lydakis/mcpx/releases/download').replace(/\/+$/, '');
}

function releaseTag(version) {
  const tagPrefix = process.env.MCPX_GO_RELEASE_TAG_PREFIX || 'v';
  return `${tagPrefix}${version}`;
}

function releaseAssetURL(version, goos, goarch) {
  const baseURL = releaseBaseURL();
  const tag = releaseTag(version);
  const asset = releaseAssetName(version, goos, goarch);
  return `${baseURL}/${tag}/${asset}`;
}

function releaseChecksumsURL(version) {
  return `${releaseBaseURL()}/${releaseTag(version)}/checksums.txt`;
}

function requestClient(url, options = {}) {
  const allowHTTP = options.allowHTTP === true;
  const protocol = new URL(url).protocol;
  if (protocol === 'https:') {
    return https;
  }
  if (protocol === 'http:' && allowHTTP) {
    return http;
  }
  if (protocol === 'http:') {
    throw new Error(`insecure release URL requires an out-of-band checksum: ${url}`);
  }
  throw new Error(`unsupported protocol: ${protocol}`);
}

function parseChecksumsText(text, version) {
  const prefix = `mcpx_${version}_`;
  const checksums = {};
  for (const rawLine of String(text || '').split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line) {
      continue;
    }
    const match = line.match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/);
    if (!match) {
      continue;
    }
    const digest = match[1].toLowerCase();
    const name = path.basename(match[2].trim());
    if (!name.startsWith(prefix) || !name.endsWith('.tar.gz')) {
      continue;
    }
    checksums[name] = digest;
  }
  return checksums;
}

function releaseChecksumFromBundledManifest(version, asset, manifest) {
  if (!manifest || typeof manifest !== 'object') {
    return null;
  }
  if (manifest.version !== version) {
    return null;
  }
  if (!manifest.checksums || typeof manifest.checksums !== 'object') {
    throw new Error('invalid bundled checksum manifest: checksums map missing');
  }
  if (!manifest.checksums[asset]) {
    return null;
  }
  return normalizeSHA256(manifest.checksums[asset], asset);
}

function downloadText(url, redirects, options = {}) {
  return new Promise((resolve, reject) => {
    const client = requestClient(url, options);
    const req = client.get(url, { agent: false }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        if (redirects >= 5) {
          res.resume();
          reject(new Error(`too many redirects while downloading ${url}`));
          return;
        }

        const nextURL = new URL(res.headers.location, url).toString();
        res.resume();
        resolve(downloadText(nextURL, redirects + 1, options));
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

      let body = '';
      res.setEncoding('utf8');
      res.on('data', (chunk) => {
        body += chunk;
      });
      res.on('end', () => resolve(body));
    });

    req.on('error', reject);
  });
}

async function expectedArchiveSHA256(version, asset, options = {}) {
  const resolved = await resolveExpectedArchiveSHA256(version, asset, options);
  return resolved.sha256;
}

async function resolveExpectedArchiveSHA256(version, asset, options = {}) {
  const manifest = Object.prototype.hasOwnProperty.call(options, 'checksumManifest')
    ? options.checksumManifest
    : checksumManifest;
  const downloadTextImpl = options.downloadText || downloadText;

  if (process.env.MCPX_GO_SKIP_CHECKSUM === '1') {
    console.warn('mcpx-go: warning: checksum verification skipped (MCPX_GO_SKIP_CHECKSUM=1).');
    return { sha256: null, source: 'skip' };
  }

  const envChecksum = process.env.MCPX_GO_BINARY_SHA256;
  if (envChecksum && envChecksum.trim() !== '') {
    return { sha256: normalizeSHA256(envChecksum, 'MCPX_GO_BINARY_SHA256'), source: 'env' };
  }

  const bundled = releaseChecksumFromBundledManifest(version, asset, manifest);
  if (bundled) {
    return { sha256: bundled, source: 'bundled' };
  }

  const checksumsURL = releaseChecksumsURL(version);
  const checksums = parseChecksumsText(await downloadTextImpl(checksumsURL, 0), version);
  if (!checksums[asset]) {
    throw new Error(`missing checksum for ${asset} in release checksums at ${checksumsURL}`);
  }
  return { sha256: normalizeSHA256(checksums[asset], asset), source: 'release' };
}

function allowInsecureArchiveDownload(checksumSource) {
  return checksumSource === 'env' || checksumSource === 'bundled';
}

function normalizeSHA256(value, label) {
  const normalized = String(value || '').trim().toLowerCase();
  if (!/^[a-f0-9]{64}$/.test(normalized)) {
    throw new Error(`invalid SHA-256 digest for ${label}`);
  }
  return normalized;
}

function sha256File(filePath) {
  return new Promise((resolve, reject) => {
    const hash = crypto.createHash('sha256');
    const input = fs.createReadStream(filePath);
    input.on('error', reject);
    input.on('data', (chunk) => hash.update(chunk));
    input.on('end', () => resolve(hash.digest('hex')));
  });
}

function dataHome() {
  const xdgDataHome = process.env.XDG_DATA_HOME;
  if (xdgDataHome && xdgDataHome.trim() !== '') {
    return xdgDataHome.trim();
  }
  return path.join(os.homedir(), '.local', 'share');
}

function installManPage(vendorDir) {
  const sourcePath = path.join(vendorDir, 'man', 'man1', 'mcpx.1');
  if (!fs.existsSync(sourcePath)) {
    return;
  }

  const targetDir = path.join(dataHome(), 'man', 'man1');
  const targetPath = path.join(targetDir, 'mcpx.1');

  fs.mkdirSync(targetDir, { recursive: true });
  fs.copyFileSync(sourcePath, targetPath);
  fs.chmodSync(targetPath, 0o644);
}

function download(url, destination, redirects, options = {}) {
  return new Promise((resolve, reject) => {
    const client = requestClient(url, options);
    const req = client.get(url, { agent: false }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        if (redirects >= 5) {
          res.resume();
          reject(new Error(`too many redirects while downloading ${url}`));
          return;
        }

        const nextURL = new URL(res.headers.location, url).toString();
        res.resume();
        resolve(download(nextURL, destination, redirects + 1, options));
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

async function installDownloadedArchive(archivePath, vendorDir, binaryPath, asset, expectedSHA256, options = {}) {
  const fsImpl = options.fs || fs;
  const logger = options.logger || console;
  const sha256FileImpl = options.sha256File || sha256File;
  const spawnSyncImpl = options.spawnSync || spawnSync;
  const installManPageImpl = options.installManPage || installManPage;

  try {
    if (expectedSHA256) {
      const actualSHA256 = await sha256FileImpl(archivePath);
      if (actualSHA256 !== expectedSHA256) {
        throw new Error(`checksum mismatch for ${asset}: expected ${expectedSHA256}, got ${actualSHA256}`);
      }
      logger.log(`mcpx-go: verified sha256 for ${asset}`);
    }

    const extract = spawnSyncImpl('tar', ['-xzf', archivePath, '-C', vendorDir], { stdio: 'inherit' });
    if (extract.error) {
      throw new Error(`failed to run tar: ${extract.error.message}`);
    }
    if (extract.status !== 0) {
      throw new Error(`tar exited with status ${extract.status}`);
    }
    if (!fsImpl.existsSync(binaryPath)) {
      throw new Error('archive extraction did not produce mcpx binary');
    }
    fsImpl.chmodSync(binaryPath, 0o755);
    try {
      installManPageImpl(vendorDir);
    } catch (error) {
      logger.warn(`mcpx-go: warning: failed to install man page: ${error.message}`);
    }
  } finally {
    try {
      fsImpl.unlinkSync(archivePath);
    } catch (_error) {
      // Ignore cleanup failures so install errors surface clearly.
    }
  }
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

  const asset = releaseAssetName(version, goos, goarch);
  const checksumInfo = await resolveExpectedArchiveSHA256(version, asset);

  fs.mkdirSync(vendorDir, { recursive: true });

  const url = releaseAssetURL(version, goos, goarch);
  const archivePath = path.join(os.tmpdir(), `mcpx-go-${version}-${process.pid}-${Date.now()}.tar.gz`);

  console.log(`mcpx-go: downloading ${url}`);
  await download(url, archivePath, 0, { allowHTTP: allowInsecureArchiveDownload(checksumInfo.source) });
  await installDownloadedArchive(archivePath, vendorDir, binaryPath, asset, checksumInfo.sha256);
}

module.exports = {
  allowInsecureArchiveDownload,
  download,
  downloadText,
  expectedArchiveSHA256,
  install,
  installDownloadedArchive,
  normalizeSHA256,
  parseChecksumsText,
  releaseAssetName,
  releaseAssetURL,
  releaseBaseURL,
  releaseChecksumsURL,
  releaseChecksumFromBundledManifest,
  releaseTag,
  releaseVersion,
  resolveExpectedArchiveSHA256,
  requestClient,
  resolveGOARCH,
  resolveGOOS,
  sha256File,
};

if (require.main === module) {
  install().catch((error) => {
    console.error(`mcpx-go: ${error.message}`);
    process.exit(1);
  });
}
