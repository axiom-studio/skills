// Safe, provider-neutral explanations. Never copy page text, login field values,
// screenshots, or browser errors into chat/model context to explain a handoff.
const REASONS = Object.freeze({
  authentication: 'I need you to sign in before I can continue.',
  verification: 'This step needs your verification code or approval.',
  challenge: 'The website requires a human verification step.',
  manual_confirmation: 'This step needs your direct confirmation in the browser.',
  // Agent-requested handoffs before sensitive or irreversible steps.
  payment: 'This step involves a payment. Please review and complete it yourself.',
  submit: 'This step submits something that cannot easily be undone. Please review and confirm it yourself.',
  login: 'This site needs you to sign in. Please sign in yourself; I never handle your password.',
  personal_data: 'This step needs your personal details. Please enter them yourself.',
  destructive: 'This step deletes or changes something permanently. Please confirm it yourself.',
  captcha: 'The website requires a human verification step.',
  other: 'This step needs you in the browser.',
});

export const BROWSER_HANDOFF_REASONS = Object.freeze(Object.keys(REASONS));

// An agent may add its own short explanation. It is model-authored chat text,
// bounded and stripped of control characters; it never carries page content
// captured by this runtime.
export function browserIntervention(reason, summary) {
  if (!Object.hasOwn(REASONS, reason)) throw new Error('Unsupported browser intervention reason');
  if (summary !== undefined && (typeof summary !== 'string' || summary.length > 500)) {
    throw new Error('Invalid browser intervention summary');
  }
  const text = summary?.replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim();
  return { type: 'browser_handoff', reason, summary: text || REASONS[reason], actionLabel: 'Take control' };
}

async function visible(resolve) {
  try { return await resolve().first().isVisible(); } catch { return false; }
}

// A failed task alone is not evidence that human intervention is needed. Skills
// may also request manual_confirmation explicitly at a known workflow boundary.
export async function detectBrowserIntervention(page) {
  if (await visible(() => page.getByRole('textbox', { name: /^(verification code|one.time code|security code|enter code)$/i }))) {
    return 'verification';
  }
  if (await visible(() => page.locator('iframe[title*="reCAPTCHA"], iframe[title*="hCaptcha"], iframe[title*="Human verification"]'))) {
    return 'challenge';
  }
  // A navigation link saying "Sign in" is not evidence of a blocked task.
  if (await visible(() => page.locator('input[type="password"]')) ||
    await visible(() => page.getByRole('textbox', { name: /^(email|email or phone|username)$/i }))) {
    return 'authentication';
  }
  return undefined;
}
