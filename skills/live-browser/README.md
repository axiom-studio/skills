# Live browser Skill

`skill-live-browser` is the agent's interactive browser: Camoufox (Firefox)
running headful on a private Xvfb display, streamed live to Seal Chat, with
an always-available **Take control** for the user. It is built on the shared
runtime in [`skills/_lib/live-browser`](../_lib/live-browser/README.md).
For reading, searching and comparing pages use the Lightpanda browser Skill
(`skill-browser`).

## Actions

| Action | Risk | Notes |
| --- | --- | --- |
| `live-browser-start {url?, intent?}` | read | Registers a Cortex browser session and returns `sessionId`. Declares `host:browser:session` and `host:browser:audio`: the session's registration origin carries the audio permission for its lifetime. Same run: reused. New run in the same conversation (for example the turn after the user replied with a one-time code): reattaches to the open browser and its page; the extra registration is revoked. A reattach never navigates, whatever `url` says: it returns the current page with `reattached: true` and a `message` (naming the pending bank code page when a payment waits for it); `live-browser-navigate` goes elsewhere. Another conversation's browser open: waits (up to two minutes) for it to close. |
| `live-browser-navigate {sessionId, url, intent?}` | read | |
| `live-browser-snapshot {sessionId, includeScreenshot?, intent?}` | read | Text plus elements with generation-scoped refs `sN:eM` (same shape as the old `camoufox-snapshot`). On a checkout, the final pay / place-order buttons ("Place your order", "Pay with …", "Pay ₹X", "Confirm and pay", ...; never links, "Continue" or "Use this payment method") carry `finalPay: true`. While a payment of this session (`live-browser-pay`, last 15 minutes) waits for the bank, every frame is checked: a bank one-time code field (in the issuer's frame or on its own page) adds `paymentChallenge: "otp_required"`, bank-app approval wording `paymentChallenge: "approve_in_app"`, each with `message`, the same instruction `live-browser-pay` gives (ask in chat "Might have gotten an OTP, please provide", then `live-browser-submit-payment-code`). Text-only captcha/security-check wording on such a page does not hand off. `cardFields` is true when the page (top document, same-site frames, known processor frames) has a card number field, i.e. asks for new card details. |
| `live-browser-click {sessionId, target \| generation+x+y, intent}` | write | Refuses a final pay / place-order / buy-now button (also by coordinates, including inside frames) on a checkout or after a card was filled: that is `live-browser-pay`. |
| `live-browser-fill {sessionId, target, value, intent}` | write | Works on inputs, textareas and contenteditable rich-text editors (click to focus, then type). Password, one-time code, payment and identity fields are never filled by the model. |
| `live-browser-sign-in {sessionId, credential?, oneTimeCode?, intent?}` | write | Signs in with a saved website login (see Credentials). Returns `submitted`, `otp_required`, `choose_login` (`logins`: names) or `no_matching_login`. |
| `live-browser-fill-payment-card {sessionId, amount, currency, intent?}` | write | `amount` (> 0) is the card charge: the page's order total after any balance, wallet or gift card; the spend cap is checked against it and the page must not charge more. Fills the saved card into the checkout's card fields; never submits. First checks the page has card-entry fields: a page that only lists the site's saved cards or other payment methods (Amazon's "Use this payment method", wallets, UPI) returns `not_actionable` ("This page has no card fields"; select the saved method with `live-browser-click`, then `live-browser-pay`) before any card is looked at. Returns `card_filled` (`filled`: field kinds), `amount_mismatch`, `spend_cap_exceeded` or `no_payment_card` (only on a page with card fields). |
| `live-browser-pay {sessionId, amount, currency, merchant, paidWith?, target?, intent}` | write | Declares `review: always`: the host asks the user to approve every call in chat, in every approval mode including Skip; the approval names the amount, merchant, `paidWith` (e.g. 312.00 INR from Amazon Pay balance) and the released card. `amount` is the whole order cost across payment methods: the page's order total plus the balances, wallets and gift cards applied before it (an order paid fully from a balance costs its full price; `0` only for a page showing a zero-cost order). Inputs are checked before the approval: amount/currency/merchant formats, and `target` must be an element marked `finalPay` in the latest snapshot (`x-openseal-observationRef`). Clicks that button (or, without `target`, the page's one final pay button; identical top and bottom copies count once) after re-checking the top-level origin (against `merchant` and the site where the card was filled) and the order cost. Also finds the pay button inside a known processor's checkout frame (Razorpay, Stripe, Adyen, Braintree, PayU, Cashfree, ...), which wins over the page's own; such a frame may charge at most `amount`. After the click it watches the page for up to 45 s (polling every frame each second, through "Processing your request" pages and redirects) and returns as soon as the outcome is clear: `confirmed` (`orderReference`, `summary` of the confirmation page, untrusted text, `payments` as the page showed them), `otp_required` (a bank code field on the page or in any frame: the agent asks in chat), `approve_in_app` (the agent asks the user in chat to approve in their bank app), `payment_failed` (the page says the payment failed or was declined); after 45 s `payment_verification` (other bank wording without a code field) or `clicked` (message says whether the page is still processing or did not change). Confirmation or failure wording that was already on the checkout before the click does not count. `merchant_mismatch` or `amount_mismatch` before any click. |
| `live-browser-submit-payment-code {sessionId, oneTimeCode, intent?}` | write | No extra review: the payment was approved. Within 15 minutes of `live-browser-pay`, while the top-level origin is the checkout or the page the payment led to, types the code into the code field of any frame of the page (bank 3-D Secure frames have any origin; one box per character when the bank splits it), re-checking the origin and frame before every keystroke batch, submits, watches the result like `live-browser-pay` and returns the same statuses (`otp_required` with `retry` when a fresh code field comes back). A top-level page the payment led to (the issuer's own page) counts as the payment's unless the agent navigated since `live-browser-pay`. |
| `live-browser-select {sessionId, target, value, intent}` | write | |
| `live-browser-scroll {sessionId, dx?, dy?}` | read | |
| `live-browser-screenshot {sessionId, fullPage?}` | read | Refused (and snapshot screenshots withheld) while a filled card is on the page. |
| `live-browser-request-handoff {sessionId, reason, summary}` | read | `reason`: payment, submit, login, personal_data, destructive, captcha, other. Returns `requiresHuman: true` with `challenges: ["manual_confirmation"]`. |
| `live-browser-close {sessionId}` | read | Closes Camoufox (the profile is flushed to the volume), frees the browser for the next task and revokes the session. |
| `live-browser-listen {sessionId, state: on\|off, speakerLabel?, displayName?, wakePhrases?, speakReplies?, transcriptionModel?, speechModel?, voice?}` | write | Axiom speech gateway; models default to the agent's catalog. |
| `live-browser-speak {sessionId, text, speechModel?, voice?}` | write | Axiom speech gateway. |

While a human holds control, model actions wait (bounded) and return
`status: paused_by_user` without acting; if the user is still in control the
result has `requiresHuman: true` so the run waits for hand-back. CAPTCHA and
bot-check pages trigger an automatic handoff. One-time code pages do not:
snapshots report `challenges: ["mfa"]` and the agent either signs in with
`live-browser-sign-in` (code asked for in chat) or hands off, as its
instructions say. A bank's payment code after `live-browser-pay` is reported
explicitly instead (`otp_required` from pay, or `paymentChallenge` on a
snapshot) and is never handed off: the agent asks in chat and calls
`live-browser-submit-payment-code`.

## Lifetime

There is no model-chosen duration. A browser closes after 30 minutes without
actions; every action slides the window. While it waits for the user (a
handoff in `awaiting_user`, a human in control, an action in flight, or up to
an hour after `otp_required`) it is kept open instead, and the Cortex session
is extended (`POST sessions/{id}/extend {durationMinutes}` with the session
grant, reply `{expiresAt, grant?}`) on entering `awaiting_user`, while
waiting, and ahead of each expiry. A session closes at 8 hours, or when Cortex
will not extend it before it expires.

## Credentials

Credential values reach the runtime only through gRPC `bindings`, never
through model input, and are never logged, returned, put in errors or
snapshots. Optional manifest slots:

- `website-login-1` ... `website-login-8` (`http_basic_auth`): each binding
  value is a JSON object `{username, password, website, totpSecret?, name?}`.
  `website` lists origins (`https://www.amazon.in, https://amazon.in`). A
  login is used when the page's top-level URL has the same scheme and
  registrable domain (eTLD+1 from the public suffix list, private suffixes
  included, as password managers match) as one of them: a login for
  `https://amazon.in` fills on `https://www.amazon.in`, never on
  `http://amazon.in` or another registrable domain. A missing login is
  requested for that site (`https://amazon.in`). The page's exact origin is
  re-checked immediately before every keystroke batch and click, so a
  redirect mid sign-in stops it. `totpSecret` (base32 or an `otpauth://totp`
  URI) answers authenticator-code pages (RFC 6238, computed locally); SMS or
  email codes return `otp_required` and the agent asks for the code in chat.
  Username, password and code go only into fields of the top-level document.
- `payment-card` (`payment_card`): `{cardholderName, number, expiryMonth,
  expiryYear, cvc, billingPostalCode?}`, or, when the host refuses to release
  the card for the declared `amount`/`currency` (spend cap),
  `{error: "spend_cap_exceeded", remaining, cap, currency}` with no card
  values. The host releases (and charges the spend cap for) a card only when
  this browser's latest action was a snapshot with `cardFields: true`;
  otherwise it sends `{status: "no_card_fields"}` and nothing is deducted. Card values go only into recognized card fields (autocomplete
  `cc-*`, card-specific labels) of the top document, same-site frames and
  known processor frames (Stripe, Razorpay, Adyen, Braintree, ...); a postal
  code only next to card fields. If the page's order total is higher than
  `amount`, nothing is filled (`amount_mismatch`).

`no_matching_login`, `no_payment_card` and `spend_cap_exceeded` carry a
`credentialRequest` with the exact arguments for
`openseal.skills.request_credential` (`kind` `website_login` with the page
`website` origin, or `payment_card`, and a `reason`). The agent calls it so
the user adds the login or card, or raises the cap, in their vault through an
in-chat card, and the work resumes when they save it. The live browser is
built in, so these are never Skill setup (`request_setup`) and never a
handoff.

An element the model cannot use (hidden or collapsed, covered by an overlay,
stale reference, not editable, option missing, timed out waiting for it)
returns a successful result `{status: "not_actionable", reason, hint, url,
title, browserStatus}` and changes nothing, so the agent can expand, scroll or
re-snapshot instead of failing the run. Browser faults (closed page or
context, crash) and session or permission failures remain errors.

Joining a video call is an ordinary task described in the Skill instructions
(navigate, type the display name, Ask to join, wait for admission, listen);
there is no site-specific code.

## Host interface

- gRPC `axiom.skill.v1.SkillService` and `axiom.browser.v1.BrowserControlService`
  (Control, Video, Desktop) on `SKILL_PORT` (50051).
- `CORTEX_BROWSER_API_URL` (default
  `http://sentinel.axiomcd.svc.cluster.local/orchestrator/agent/browser/v1/`)
  for session registration, human-command authorization, handoff notices,
  conversation/transcript posts, speech grants and profile-command
  authorization. `AXIOM_SPEECH_API_URL` (default
  `http://axiomcloud.axiomcd.svc.cluster.local/rest/v1/llm-gateway/v1/`) for
  transcription and speech. `CORTEX_TENANT_ID`: the tenant this runtime
  serves (set by Cortex's Skill hosting); profile commands for any other
  tenant are refused.
- `LIVE_BROWSER_PROXY_SERVER`, `LIVE_BROWSER_PROXY_USERNAME`,
  `LIVE_BROWSER_PROXY_PASSWORD` (optional): the upstream proxy for page
  traffic. Cortex injects them into every tenant's runtime from the platform
  Secret `live-browser-proxy` in the Skill namespace; without them the browser
  connects directly. They are not passed on to the browser process.
- `BrowserControlService.Control` also takes two session-less profile
  commands, `{"type":"profileStatus"}` (returns `{state, origins, updatedAt,
  sizeBytes}`) and `{"type":"forgetProfile"}` (stops the open browser cleanly,
  deletes the profile and returns `{deleted}`), each with a single-command
  proof that Cortex verifies at `profile/authorize`.

## Browser profile

The runtime is per tenant and keeps the tenant's one real Camoufox profile on
its persistent volume (`browser-profile`, 10Gi, mounted at
`/var/lib/axiom-live-browser`, override with `LIVE_BROWSER_PROFILE_ROOT`).
The browser is launched with Playwright's persistent context on
`<root>/camoufox`, so everything a normal browser keeps survives between
sessions and pod restarts: first- and third-party cookies, localStorage,
IndexedDB (including non-extractable CryptoKeys, as WhatsApp Web uses), Cache
Storage, service workers, history, permissions, site settings and saved
logins. Nothing is exported, uploaded or merged; there is no central copy.

- One browser per profile. Firefox opens a profile only once, and a live
  browser owns its display (live view and take control) and its page audio,
  so sessions are queued: one live browser at a time, and a start from
  another conversation waits in FIFO order (up to two minutes) for it to
  close. A new start in the same conversation reattaches to its browser.
- Durability: closing a browser closes Camoufox, which flushes its SQLite
  files; the profile is released only after Firefox has exited. On SIGTERM
  the runtime closes every browser before exiting (bounded to 45 s, inside
  the pod's termination grace period). The image runs under `tini`, which
  reaps orphaned browser processes.
- Stale `lock`/`.parentlock` files left by a crash or another pod are removed
  before launch (the runtime holds the profile exclusively).
- The Camoufox fingerprint generated on first launch is kept in
  `identity.json` and reused, so sites see the same device every time, until
  the Camoufox build changes.
- Saved sign-ins: status reports `profile: {state, origins}`, where origins
  are the top-level sites (eTLD+1) the browser has visited, kept in
  `sites.json`. Third-party sites whose cookies the browser keeps are not
  listed. Forget sign-ins (`forgetProfile`) deletes `camoufox/`, `sites.json`
  and `identity.json`.

Never log page content, typed values, cookies, storage, tokens or audio.

## Development

The library is installed with `install-links=true` (a copy, so its own
dependencies resolve here). After editing `skills/_lib/live-browser`, run
`npm install` again in this directory.

```bash
npm --prefix skills/_lib/live-browser ci && npm --prefix skills/_lib/live-browser test
npm --prefix skills/live-browser ci && npm --prefix skills/live-browser test
docker build -f skills/live-browser/Dockerfile --build-arg SKILL_NAME=live-browser -t axiomstudio/skill-live-browser:1.0.13 .
```
