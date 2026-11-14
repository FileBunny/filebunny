# Mail relay type: `mailjet-api`

For servers whose provider blocks outgoing SMTP (465/587): the backend
sends through [Mailjet](https://www.mailjet.com)'s Send API v3.1 over
HTTPS, so only port 443 is needed. Code: `backend/internal/mail/api.go`,
`backend/internal/server/contacts.go`. The general relay setup (the
secret file, limits, DNS) is in
[self-hosting.md](../self-hosting.md#e-mail).

## Configuration

The relay takes no `host`, `port` or `implicit_tls`; `username` and
`password` are the API key and secret (the same as Mailjet's SMTP
credentials):

```json
{"name": "mailjet", "type": "mailjet-api",
 "username": "<API key>", "password": "<secret key>", "daily_limit": 200}
```

**Use a Mailjet account for FileBunny only**: the contact cleanup below
deletes every contact in it.

## How it sends

- One fixed URL, `https://api.mailjet.com/v3.1/send`; nothing from the
  client goes into the request but the checked recipient.
- No redirects, no proxy from the environment, TLS 1.2+ with a verified
  certificate; the response is capped and only its status is kept (it
  may quote the recipient).
- Open and click tracking off in every message.
- An API error never counts as a refused recipient, so the next relay is
  tried.

## Contacts

Mailjet keeps every recipient as a contact, through SMTP and the API
alike. With `"delete_contacts": true` (the default; `false` keeps them)
the backend removes them once a day, first a minute after the start:

- It lists the contacts (paged, response capped) and deletes those older
  than a day with Mailjet's GDPR delete (`DELETE /v4/contacts/{id}`):
  anonymized at once, gone after 30 days.
- At most 2000 deletions per relay and day, one request at a time with a
  pause (Mailjet publishes no rate limits); a rate limit (429) or 5
  failures end the run until the next day.
- Addresses that unsubscribed through Mailjet's own link or reported spam
  go on FileBunny's block list first (as an HMAC).
- Only counts are logged.

For an SMTP relay at Mailjet, delete contacts in its dashboard.

## Unsubscribe header

Mailjet replaces FileBunny's `List-Unsubscribe` header with its own. An
address that unsubscribes that way lands on Mailjet's suppression list,
not on FileBunny's: Mailjet then accepts and drops further mail to it,
and the sender still sees "Sent". The block link in the mail text keeps
working, and the cleanup above copies such addresses to the block list.

Whether a GDPR delete also clears Mailjet's suppression of an address is
not documented.

## Tests

`TestMailjetAPIRequest`, `TestMailjetAPIErrors`,
`TestMailjetAPINoRedirect`, `TestLoadConfigRelayTypes`,
`TestMailjetContacts`, `TestCleanupContacts*`,
`TestDeleteContactsOnlyForAPIRelays`.
