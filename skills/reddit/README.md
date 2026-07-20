# Reddit Skill

Atlas gRPC skill for Reddit OAuth and the complete Reddit Data API surface.

## Actions

- `reddit-authorize-url` builds a user authorization URL with explicit state, duration, and scopes.
- `reddit-token` supports `authorization_code`, `refresh_token`, `password`, `client_credentials`, and Reddit's installed-client grant.
- `reddit-api-request` calls any relative endpoint on `https://oauth.reddit.com` with GET, POST, PUT, PATCH, DELETE, HEAD, or OPTIONS and JSON, form, multipart, or raw bodies. Responses can be decoded automatically, returned as text, or base64-encoded.
- `reddit-health` reports process health.

The generic request action intentionally covers the live endpoint catalog without hard-coding a stale subset. Use paths from Reddit's official API reference, such as `/api/v1/me`, `/r/golang/new`, `/api/submit`, `/api/comment`, `/api/vote`, `/api/search_reddit_names`, `/api/mod/conversations`, and the moderation, flair, wiki, live-thread, collections, emoji, chat, and account endpoints. OAuth scopes and Reddit-side role checks still apply.

## Multipart bodies

String values become ordinary fields. A file value is an object containing `filename` and base64-encoded `contentBase64`:

```json
{
  "filepath": {
    "filename": "image.png",
    "contentBase64": "iVBORw0KGgo..."
  },
  "mimetype": "image/png"
}
```

## Operational requirements

Use a Reddit-approved application, a stable descriptive User-Agent, least-privilege OAuth scopes, and the returned `rateLimit` fields. The action never accepts an absolute URL, so credentials cannot be routed away from Reddit. It also prevents callers from overriding authentication, cookies, host routing, and User-Agent headers.

Reddit requires deletion of stored content and identifying user data when the source content or account is deleted. Systems persisting API results must implement that lifecycle outside this stateless transport skill.
