// Centralised in-app URL builders (issue #130). Navigation lives on real
// anchors now, so the destination URL has to be computed up front (where the
// route shape is known) and handed to a <Link to=...> rather than being
// assembled inside an onClick that calls navigate(). Keeping these in one
// place stops the deep-link query strings from drifting between the org
// summary and the workbench, which built the identical /detail URL by hand.

// zombieDetailHref — link to a single resource's detail view. Accepts a
// zombie-ish object carrying resource_id + internal_account_id + region +
// service (the shape the summary, workbench, and dismissed lists all share).
// resource_id goes in the path so it's encoded explicitly; the rest ride the
// query string via URLSearchParams (which percent-encodes each value).
export function zombieDetailHref({ resource_id, internal_account_id, region, service }) {
  const q = new URLSearchParams({
    account: internal_account_id ?? '',
    region: region ?? '',
    service: service ?? '',
  });
  return `/detail/${encodeURIComponent(resource_id)}?${q.toString()}`;
}

// editAccountHref — link to a cloud account's management page. Uses the
// canonical /settings/cloud-accounts/:id path directly (the bare
// /cloud-accounts/:id form only exists as a back-compat redirect), so the
// anchor lands without a redirect bounce.
export function editAccountHref(account) {
  return `/settings/cloud-accounts/${account.id}`;
}
