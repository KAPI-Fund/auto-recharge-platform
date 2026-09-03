'use strict';

const Module = require('node:module');
const path = require('node:path');

const legacyRoot = path.join(__dirname, 'legacy');
const originalStore = path.join(legacyRoot, 'mysql-store.js');
const originalTaxFreeAddress = path.join(legacyRoot, 'tax-free-address.js');
const originalRuntimeLog = path.join(legacyRoot, 'runtime-log.js');
const storeAdapter = require('./legacy-go/store-adapter.cjs');
const taxFreeAddressAdapter = require('./legacy-go/tax-free-address.cjs');
const runtimeLogAdapter = require('./legacy-go/runtime-log.cjs');
const originalLoad = Module._load;

Module._load = function loadWithGoStore(request, parent, isMain) {
  let resolved = '';
  try {
    resolved = Module._resolveFilename(request, parent);
  } catch (_) {
    resolved = '';
  }
  if (resolved === originalStore) return storeAdapter;
  if (resolved === originalTaxFreeAddress) return taxFreeAddressAdapter;
  if (resolved === originalRuntimeLog) return runtimeLogAdapter;
  return originalLoad.call(this, request, parent, isMain);
};

try {
  require(path.join(legacyRoot, 'index.js'));
} finally {
  Module._load = originalLoad;
}
