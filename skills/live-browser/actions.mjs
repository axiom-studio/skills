// Model-facing input schemas. Keep in sync with skill.yaml (server.test.mjs
// checks the action names and required fields).
const sessionId = { type: 'string', pattern: '^[A-Za-z0-9][A-Za-z0-9_:-]{0,127}$' };
const url = { type: 'string', pattern: '^https?://', maxLength: 2048 };
const intent = { type: 'string', minLength: 3, maxLength: 500 };
const target = { type: 'string', pattern: '^s[1-9][0-9]*:e[1-9][0-9]*$' };
const label = { type: 'string', minLength: 1, maxLength: 100 };
const object = (properties, required = []) => ({ type: 'object', additionalProperties: false, properties, required });

export const REASONS = ['payment', 'submit', 'login', 'personal_data', 'destructive', 'captcha', 'other'];

export const schemas = Object.freeze({
  'live-browser-start': object({ url, intent }),
  'live-browser-navigate': object({ sessionId, url, intent }, ['sessionId', 'url']),
  'live-browser-snapshot': object({ sessionId, includeScreenshot: { type: 'boolean', default: false }, intent }, ['sessionId']),
  'live-browser-click': {
    ...object({ sessionId, target, generation: { type: 'integer', minimum: 1 }, x: { type: 'number', minimum: 0, maximum: 10000 },
      y: { type: 'number', minimum: 0, maximum: 10000 }, intent }, ['sessionId', 'intent']),
    oneOf: [{ required: ['target'] }, { required: ['generation', 'x', 'y'] }],
  },
  'live-browser-fill': object({ sessionId, target, value: { type: 'string', maxLength: 20000 }, intent }, ['sessionId', 'target', 'value', 'intent']),
  'live-browser-select': object({ sessionId, target, value: { type: 'string', minLength: 1, maxLength: 1000 }, intent }, ['sessionId', 'target', 'value', 'intent']),
  'live-browser-scroll': object({ sessionId, dx: { type: 'number', minimum: -10000, maximum: 10000, default: 0 },
    dy: { type: 'number', minimum: -10000, maximum: 10000, default: 0 } }, ['sessionId']),
  'live-browser-screenshot': object({ sessionId, fullPage: { type: 'boolean', default: false } }, ['sessionId']),
  'live-browser-request-handoff': object({ sessionId, reason: { type: 'string', enum: REASONS },
    summary: { type: 'string', minLength: 1, maxLength: 500 } }, ['sessionId', 'reason', 'summary']),
  'live-browser-sign-in': object({ sessionId, credential: { type: 'string', minLength: 1, maxLength: 200 },
    oneTimeCode: { type: 'string', pattern: '^[A-Za-z0-9][A-Za-z0-9 -]{2,14}[A-Za-z0-9]$' }, intent }, ['sessionId']),
  'live-browser-fill-payment-card': object({ sessionId, amount: { type: 'string', pattern: '^(0|[1-9][0-9]{0,11})(\\.[0-9]{1,4})?$' },
    currency: { type: 'string', pattern: '^[A-Z]{3}$' }, intent }, ['sessionId', 'amount', 'currency']),
  'live-browser-pay': object({ sessionId, amount: { type: 'string', pattern: '^(0|[1-9][0-9]{0,11})(\\.[0-9]{1,4})?$' },
    currency: { type: 'string', pattern: '^[A-Z]{3}$' }, merchant: { type: 'string', pattern: '^https?://[^/?#\\s]+/?$', maxLength: 300 },
    target, intent }, ['sessionId', 'amount', 'currency', 'intent']),
  'live-browser-close': object({ sessionId }, ['sessionId']),
  'live-browser-listen': object({ sessionId, state: { type: 'string', enum: ['on', 'off'] }, speakerLabel: label, displayName: label,
    wakePhrases: { type: 'array', maxItems: 8, items: label }, speakReplies: { type: 'boolean', default: true },
    transcriptionModel: { type: 'string', minLength: 1, maxLength: 200 },
    speechModel: { type: 'string', minLength: 1, maxLength: 200 }, voice: { type: 'string', minLength: 1, maxLength: 100 } }, ['sessionId', 'state']),
  'live-browser-speak': object({ sessionId, text: { type: 'string', minLength: 1, maxLength: 3000 },
    speechModel: { type: 'string', minLength: 1, maxLength: 200 }, voice: { type: 'string', minLength: 1, maxLength: 100 } }, ['sessionId', 'text']),
});

// Minimal structural validation of model input before it reaches the browser.
export function validateInput(action, input) {
  const schema = schemas[action];
  if (!schema) throw new Error('unknown live browser action');
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('input must be an object');
  for (const key of Object.keys(input)) if (!Object.hasOwn(schema.properties, key)) throw new Error(`unknown field ${key}`);
  for (const key of schema.required) if (input[key] === undefined) throw new Error(`${key} is required`);
  for (const [key, value] of Object.entries(input)) {
    const rule = schema.properties[key];
    const kind = rule.type === 'integer' ? Number.isInteger(value) : rule.type === 'number' ? Number.isFinite(value)
      : rule.type === 'array' ? Array.isArray(value) : typeof value === rule.type;
    if (!kind) throw new Error(`${key} has the wrong type`);
    if (typeof value === 'string' && ((rule.maxLength && value.length > rule.maxLength) || (rule.minLength && value.length < rule.minLength) ||
      (rule.pattern && !new RegExp(rule.pattern).test(value)) || (rule.enum && !rule.enum.includes(value)))) throw new Error(`${key} is invalid`);
    if (typeof value === 'number' && ((rule.minimum !== undefined && value < rule.minimum) || (rule.maximum !== undefined && value > rule.maximum))) {
      throw new Error(`${key} is out of range`);
    }
    if (Array.isArray(value) && (value.length > rule.maxItems || value.some(item => typeof item !== 'string' || !item.length || item.length > rule.items.maxLength))) {
      throw new Error(`${key} is invalid`);
    }
  }
  if (schema.oneOf && schema.oneOf.filter(option => option.required.every(key => input[key] !== undefined)).length !== 1) {
    throw new Error('provide either target or generation with x and y');
  }
  return input;
}
