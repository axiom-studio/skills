// Safe, provider-neutral explanations. Never copy page text, login field values,
// screenshots, or browser errors into chat/model context to explain a handoff.
const REASONS = Object.freeze({
  authentication: 'I need you to sign in before I can continue.',
  verification: 'This step needs your verification code or approval.',
  challenge: 'The website requires a human verification step.',
  manual_confirmation: 'This step needs your direct confirmation in the browser.',
});

export function browserIntervention(reason) {
  if (!Object.hasOwn(REASONS, reason)) throw new Error('Unsupported browser intervention reason');
  return { type: 'browser_handoff', reason, summary: REASONS[reason], actionLabel: 'Take control' };
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
