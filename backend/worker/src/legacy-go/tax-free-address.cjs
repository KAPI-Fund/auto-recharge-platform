'use strict';

const store = require('./store-adapter.cjs');

const US_STATE_LABELS = Object.freeze({
  AL: 'Alabama', AK: 'Alaska', AZ: 'Arizona', AR: 'Arkansas', CA: 'California', CO: 'Colorado',
  CT: 'Connecticut', DE: 'Delaware', FL: 'Florida', GA: 'Georgia', HI: 'Hawaii', ID: 'Idaho',
  IL: 'Illinois', IN: 'Indiana', IA: 'Iowa', KS: 'Kansas', KY: 'Kentucky', LA: 'Louisiana',
  ME: 'Maine', MD: 'Maryland', MA: 'Massachusetts', MI: 'Michigan', MN: 'Minnesota', MS: 'Mississippi',
  MO: 'Missouri', MT: 'Montana', NE: 'Nebraska', NV: 'Nevada', NH: 'New Hampshire', NJ: 'New Jersey',
  NM: 'New Mexico', NY: 'New York', NC: 'North Carolina', ND: 'North Dakota', OH: 'Ohio', OK: 'Oklahoma',
  OR: 'Oregon', PA: 'Pennsylvania', RI: 'Rhode Island', SC: 'South Carolina', SD: 'South Dakota',
  TN: 'Tennessee', TX: 'Texas', UT: 'Utah', VT: 'Vermont', VA: 'Virginia', WA: 'Washington',
  WV: 'West Virginia', WI: 'Wisconsin', WY: 'Wyoming', DC: 'District of Columbia'
});

function normalizeUsStateName(value) {
  const raw = String(value || '').trim();
  return US_STATE_LABELS[raw.toUpperCase()] || raw;
}

function generateRandomUsTaxFreeAddress() {
  const states = ['OR', 'MT', 'NH', 'DE', 'AK'];
  const state = states[Math.floor(Math.random() * states.length)];
  const cityByState = { OR: 'Portland', MT: 'Billings', NH: 'Manchester', DE: 'Wilmington', AK: 'Anchorage' };
  return {
    line1: `${100 + Math.floor(Math.random() * 8900)} Main Street`,
    city: cityByState[state],
    state,
    postal_code: state === 'OR' ? '97205' : state === 'MT' ? '59101' : state === 'NH' ? '03101' : state === 'DE' ? '19801' : '99501',
    country: 'US',
    generated: true
  };
}

async function pickTaxFreeAddress(region, lastUsedId = null) {
  const addresses = await store.pickableTaxFreeAddresses(String(region || ''));
  if (addresses.length === 0) throw new Error('该地区无可用免税地址模板');
  if (addresses.length === 1) {
    await store.setAppConfigValue('last_used_address_id', String(addresses[0].id));
    return addresses[0];
  }
  const candidates = lastUsedId == null
    ? addresses
    : addresses.filter((item) => String(item.id) !== String(lastUsedId));
  const pool = candidates.length ? candidates : addresses;
  const picked = pool[Math.floor(Math.random() * pool.length)];
  await store.setAppConfigValue('last_used_address_id', String(picked.id));
  return picked;
}

async function pickBillingAddressForCheckout(lastUsedId = null) {
  let picked;
  try {
    picked = await pickTaxFreeAddress('US', lastUsedId);
  } catch (_) {
    picked = { id: 0, ...generateRandomUsTaxFreeAddress() };
  }
  return { ...picked, state: normalizeUsStateName(picked.state) };
}

async function markAddressBound(addressId, cardId) {
  const id = String(addressId || '').trim();
  const card = String(cardId || '').trim();
  if (!id || !card) return { success: false };
  return store.bindTaxFreeAddress(id, card);
}

module.exports = {
  pickBillingAddressForCheckout,
  markAddressBound,
  normalizeUsStateName,
  generateRandomUsTaxFreeAddress,
  US_STATE_LABELS
};
