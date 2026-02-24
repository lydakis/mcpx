#!/usr/bin/env node

'use strict';

const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');

const bundledBinary = path.join(__dirname, '..', 'vendor', 'mcpx');
const command = fs.existsSync(bundledBinary) ? bundledBinary : 'mcpx';

const child = spawn(command, process.argv.slice(2), { stdio: 'inherit' });

child.on('error', (error) => {
  if (error.code === 'ENOENT') {
    console.error('mcpx-go: no binary found. Reinstall the package or install `mcpx` in PATH.');
  } else {
    console.error(`mcpx-go: failed to start ${command}: ${error.message}`);
  }
  process.exit(1);
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
