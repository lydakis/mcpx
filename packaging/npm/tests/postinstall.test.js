const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const postinstall = require('../lib/postinstall.js');

async function withEnv(overrides, fn) {
  const previous = new Map();
  for (const [key, value] of Object.entries(overrides)) {
    previous.set(key, process.env[key]);
    if (value === null) {
      delete process.env[key];
    } else {
      process.env[key] = value;
    }
  }

  try {
    return await fn();
  } finally {
    for (const [key, value] of previous.entries()) {
      if (value === undefined) {
        delete process.env[key];
      } else {
        process.env[key] = value;
      }
    }
  }
}

test('expectedArchiveSHA256 falls back to release checksums for version overrides', async () => {
  const version = '1.2.3';
  const asset = 'mcpx_1.2.3_linux_amd64.tar.gz';
  const digest = 'a'.repeat(64);

  await withEnv(
    {
      MCPX_GO_BINARY_SHA256: null,
      MCPX_GO_SKIP_CHECKSUM: null,
      MCPX_GO_RELEASE_BASE_URL: 'https://example.test/releases/download',
      MCPX_GO_RELEASE_TAG_PREFIX: 'v',
    },
    async () => {
      const got = await postinstall.expectedArchiveSHA256(version, asset, {
        checksumManifest: { version: '0.0.0', checksums: {} },
        downloadText: async (url, redirects) => {
          assert.equal(url, 'https://example.test/releases/download/v1.2.3/checksums.txt');
          assert.equal(redirects, 0);
          return `${digest}  ${asset}\n`;
        },
      });
      assert.equal(got, digest);
    },
  );
});

test('expectedArchiveSHA256 rejects insecure checksum URL without out-of-band checksum', async () => {
  const version = '1.2.3';
  const asset = 'mcpx_1.2.3_linux_amd64.tar.gz';

  await withEnv(
    {
      MCPX_GO_BINARY_SHA256: null,
      MCPX_GO_SKIP_CHECKSUM: null,
      MCPX_GO_RELEASE_BASE_URL: 'http://example.test/releases/download',
      MCPX_GO_RELEASE_TAG_PREFIX: 'v',
    },
    async () => {
      await assert.rejects(
        postinstall.expectedArchiveSHA256(version, asset, {
          checksumManifest: { version: '0.0.0', checksums: {} },
        }),
        /insecure release URL/,
      );
    },
  );
});

test('requestClient allows HTTP when caller opts in with a trusted checksum', () => {
  const http = require('http');
  const client = postinstall.requestClient('http://example.test/mcpx.tar.gz', { allowHTTP: true });
  assert.equal(client, http);
});

test('downloadText rejects HTTPS redirects to insecure checksum URLs', async () => {
  const https = require('https');
  const originalHttpsGet = https.get;

  https.get = (_url, _options, callback) => {
    callback({
      statusCode: 302,
      headers: { location: 'http://mirror.example.test/checksums.txt' },
      resume() {},
    });
    return { on() {} };
  };

  try {
    await assert.rejects(
      postinstall.downloadText('https://example.test/checksums.txt', 0),
      /insecure release URL/,
    );
  } finally {
    https.get = originalHttpsGet;
  }
});

test('installDownloadedArchive removes temp archive after checksum mismatch', async () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mcpx-postinstall-test-'));
  const archivePath = path.join(tmpDir, 'mcpx.tar.gz');
  const vendorDir = path.join(tmpDir, 'vendor');
  const binaryPath = path.join(vendorDir, 'mcpx');

  fs.mkdirSync(vendorDir, { recursive: true });
  fs.writeFileSync(archivePath, 'placeholder');

  try {
    await assert.rejects(
      postinstall.installDownloadedArchive(
        archivePath,
        vendorDir,
        binaryPath,
        'mcpx_1.2.3_linux_amd64.tar.gz',
        '0'.repeat(64),
        {
          logger: { log() {}, warn() {} },
          sha256File: async () => 'f'.repeat(64),
          spawnSync: () => {
            throw new Error('extract should not run after checksum mismatch');
          },
        },
      ),
      /checksum mismatch/,
    );
    assert.equal(fs.existsSync(archivePath), false);
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('install skips checksum lookup when bundled binary already exists', async () => {
  const binaryPath = path.join(path.dirname(require.resolve('../lib/postinstall.js')), '..', 'vendor', 'mcpx');
  const originalExistsSync = fs.existsSync;
  const https = require('https');
  const http = require('http');
  const originalHttpsGet = https.get;
  const originalHttpGet = http.get;

  fs.existsSync = (targetPath) => {
    if (targetPath === binaryPath) {
      return true;
    }
    return originalExistsSync(targetPath);
  };

  https.get = () => {
    throw new Error('checksum lookup should not run when mcpx already exists');
  };
  http.get = https.get;

  try {
    await withEnv(
      {
        MCPX_GO_BINARY_SHA256: null,
        MCPX_GO_SKIP_CHECKSUM: null,
        MCPX_GO_BINARY_VERSION: '1.2.3',
      },
      async () => {
        await postinstall.install();
      },
    );
  } finally {
    fs.existsSync = originalExistsSync;
    https.get = originalHttpsGet;
    http.get = originalHttpGet;
  }
});
