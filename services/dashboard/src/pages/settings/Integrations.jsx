import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTheme } from '../../theme/ThemeContext';
import {
  createChannel,
  deleteChannel,
  listChannelDispatches,
  listChannels,
  testChannel,
  updateChannel,
} from '../../api/client';
import { Spinner } from '../../components/primitives';

// Integrations pane: notification channels (email + Slack + Teams) that receive a
// digest after each scan. Container is gated on PERM.CHANNELS_MANAGE (admin+),
// so this whole file assumes the caller can mutate. Mirrors the SSO Connections
// pane idiom (react-query + table + modal).

const KINDS = [
  { value: 'email', label: 'Email (SMTP)' },
  { value: 'slack', label: 'Slack webhook' },
  { value: 'teams', label: 'Microsoft Teams webhook' },
];

// Email provider presets — prefill host/port so the admin picks instead of
// memorising endpoints. Pure UX sugar; the stored config stays generic SMTP.
// No "single mailbox / App Password" preset by design — that path is the
// least-best-practice one (see docs/notification-channels-runbook.md); it
// stays reachable via "Custom SMTP" + the runbook.
const EMAIL_PROVIDERS = [
  { value: 'workspace', label: 'Google Workspace relay', smtpHost: 'smtp-relay.gmail.com', smtpPort: '587' },
  { value: 'ses', label: 'Amazon SES', smtpHost: 'email-smtp.eu-central-1.amazonaws.com', smtpPort: '587' },
  { value: 'custom', label: 'Custom SMTP' },
];

function inferEmailProvider(host) {
  if (!host) return 'custom';
  if (host === 'smtp-relay.gmail.com') return 'workspace';
  if (/^email-smtp\..+\.amazonaws\.com$/.test(host)) return 'ses';
  return 'custom';
}

export default function Integrations() {
  const { isDark } = useTheme();
  const qc = useQueryClient();

  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState(null); // channel object or null
  const [deliveriesFor, setDeliveriesFor] = useState(null); // channel object or null
  const [topError, setTopError] = useState('');
  const [notice, setNotice] = useState(''); // transient success line
  // Per-row in-flight set. A single shared id would let one row's mutation
  // completing clear another row's busy state mid-flight (tests run up to 10s).
  const [busyIds, setBusyIds] = useState(() => new Set());
  const markBusy = (id) => { setTopError(''); setNotice(''); setBusyIds((prev) => new Set(prev).add(id)); };
  const clearBusy = (id) => setBusyIds((prev) => { const s = new Set(prev); s.delete(id); return s; });

  const channels = useQuery({ queryKey: ['channels'], queryFn: listChannels });
  const invalidate = () => qc.invalidateQueries({ queryKey: ['channels'] });

  const testMutation = useMutation({
    mutationFn: ({ id }) => testChannel(id),
    onMutate: ({ id }) => markBusy(id),
    onSuccess: (res, { id, label }) => {
      clearBusy(id);
      if (res?.status === 'sent') {
        setNotice(`Test message sent to "${label}".`);
      } else {
        setTopError(`Test to "${label}" failed: ${res?.error || 'unknown error'}`);
      }
    },
    onError: (err, { id }) => { clearBusy(id); setTopError(humanize(err, 'Test failed')); },
  });

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }) => updateChannel(id, { enabled }),
    onMutate: ({ id }) => markBusy(id),
    onSuccess: (_res, { id }) => { clearBusy(id); invalidate(); },
    onError: (err, { id }) => { clearBusy(id); setTopError(humanize(err, 'Failed to update channel')); },
  });

  const deleteMutation = useMutation({
    mutationFn: ({ id }) => deleteChannel(id),
    onMutate: ({ id }) => markBusy(id),
    onSuccess: (_res, { id }) => { clearBusy(id); invalidate(); },
    onError: (err, { id }) => { clearBusy(id); setTopError(humanize(err, 'Failed to delete channel')); },
  });

  return (
    <div style={{ padding: 24 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <p style={{ margin: 0, fontSize: 13, color: 'var(--color-text-mid)' }}>
          Channels that receive a savings digest after each scan. New channels start disabled — send a test, then enable.
        </p>
        <button type="button" onClick={() => { setAdding(true); setTopError(''); setNotice(''); }} style={primaryButton()}>
          Add channel
        </button>
      </div>

      {topError && <Banner color={'var(--color-error)'} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>{topError}</Banner>}
      {notice && <Banner color={'#10b981'} bg={isDark ? 'rgba(16,185,129,0.15)' : '#dcfce7'}>{notice}</Banner>}

      {channels.isPending ? (
        <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
      ) : channels.isError ? (
        <div style={{ padding: 24, color: 'var(--color-error)' }}>Failed to load channels.</div>
      ) : (channels.data || []).length === 0 ? (
        <EmptyState />
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13, marginTop: 8 }}>
          <thead>
            <tr style={{ borderBottom: `1px solid var(--color-border)` }}>
              <Th>Label</Th>
              <Th>Type</Th>
              <Th>Status</Th>
              <Th>Min savings</Th>
              <Th></Th>
            </tr>
          </thead>
          <tbody>
            {(channels.data || []).map((c) => {
              const busy = busyIds.has(c.id);
              return (
                <tr key={c.id} style={{ borderBottom: `1px solid var(--color-border)` }}>
                  <Td>{c.label || '—'}</Td>
                  <Td><code style={{ fontSize: 12 }}>{c.kind}</code></Td>
                  <Td>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => toggleMutation.mutate({ id: c.id, enabled: !c.enabled })}
                      style={{ ...ghostButton(), color: c.enabled ? '#10b981' : 'var(--color-text-muted)' }}
                      title="Toggle enabled"
                    >
                      {c.enabled ? 'enabled' : 'disabled'}
                    </button>
                  </Td>
                  <Td>${c.trigger_rule?.min_monthly_savings_usd ?? 0}/mo</Td>
                  <Td style={{ whiteSpace: 'nowrap' }}>
                    <button type="button" disabled={busy} onClick={() => testMutation.mutate({ id: c.id, label: c.label })} style={ghostButton()}>
                      {busy ? '…' : 'Test'}
                    </button>
                    <button type="button" onClick={() => setDeliveriesFor(c)} style={{ ...ghostButton(), marginLeft: 6 }}>Deliveries</button>
                    <button type="button" onClick={() => { setEditing(c); setTopError(''); setNotice(''); }} style={{ ...ghostButton(), marginLeft: 6 }}>Edit</button>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => {
                        const safe = sanitizeForDialog(c.label);
                        if (confirm(`Delete channel "${safe}"? It will stop receiving scan digests.`)) {
                          deleteMutation.mutate({ id: c.id });
                        }
                      }}
                      style={{ ...ghostButton(), color: 'var(--color-error)', marginLeft: 6 }}
                    >
                      Delete
                    </button>
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      {adding && (
        <ChannelModal mode="create" onClose={() => setAdding(false)} onSaved={() => { setAdding(false); invalidate(); }} isDark={isDark} />
      )}
      {editing && (
        // key by channel id so switching which channel is being edited
        // remounts the modal and re-initialises its form-from-props state,
        // rather than leaving the previous channel's values in the inputs.
        <ChannelModal key={editing.id} mode="edit" existing={editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); invalidate(); }} isDark={isDark} />
      )}
      {deliveriesFor && (
        <DeliveriesModal channel={deliveriesFor} onClose={() => setDeliveriesFor(null)} isDark={isDark} />
      )}
    </div>
  );
}

// ── add/edit modal ────────────────────────────────────────────────────────────

function ChannelModal({ mode, existing, onClose, onSaved, isDark }) {
  const isEdit = mode === 'edit';
  const [error, setError] = useState('');
  const [form, setForm] = useState(() => initialForm(existing));

  const set = (k) => (e) => setForm((f) => ({ ...f, [k]: e.target.value }));

  // Picking a provider preset prefills host/port; "Custom" leaves them as-is.
  const applyProvider = (e) => {
    const value = e.target.value;
    const preset = EMAIL_PROVIDERS.find((p) => p.value === value);
    setForm((f) => ({
      ...f,
      provider: value,
      ...(preset?.smtpHost ? { smtpHost: preset.smtpHost, smtpPort: preset.smtpPort } : {}),
    }));
  };

  const mutation = useMutation({
    mutationFn: () => {
      const payload = {
        label: form.label,
        triggerRule: {
          min_monthly_savings_usd: Number(form.minSavings) || 0,
          digest_top_n: Number(form.digestTopN) || 0,
          on: ['new_zombies'],
        },
        config: buildConfig(form),
      };
      if (isEdit) {
        return updateChannel(existing.id, payload);
      }
      return createChannel({ kind: form.kind, enabled: false, ...payload });
    },
    onSuccess: onSaved,
    onError: (err) => setError(humanize(err, 'Save failed')),
  });

  const submitDisabled = mutation.isPending || !form.label.trim();

  return (
    <ModalShell title={isEdit ? 'Edit channel' : 'Add channel'} onClose={onClose} lockClose={mutation.isPending} isDark={isDark}>
      <form
        onSubmit={(e) => { e.preventDefault(); setError(''); mutation.mutate(); }}
        style={{ display: 'flex', flexDirection: 'column', gap: 12 }}
      >
        <Field label="Type">
          <select value={form.kind} onChange={set('kind')} style={inputStyle()} disabled={isEdit}>
            {KINDS.map((k) => <option key={k.value} value={k.value}>{k.label}</option>)}
          </select>
        </Field>
        <Field label="Label">
          <input type="text" value={form.label} onChange={set('label')} required maxLength={120} style={inputStyle()} />
        </Field>

        {form.kind === 'slack' || form.kind === 'teams' ? (
          <Field label="Webhook URL" hint={isEdit ? 'Leave as *** (or clear) to keep the stored URL.' : (form.kind === 'slack' ? 'hooks.slack.com/...' : 'Workflows URL')}>
            <input type="text" value={form.webhookUrl} onChange={set('webhookUrl')} required={!isEdit} autoComplete="off" style={inputStyle()} />
          </Field>
        ) : (
          <>
            <Field label="Provider" hint={form.provider === 'ses' ? 'Edit the region in the host below if not eu-central-1.' : 'Prefills the host/port — you still enter username, password and From.'}>
              <select value={form.provider} onChange={applyProvider} style={inputStyle()}>
                {EMAIL_PROVIDERS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
              </select>
            </Field>
            <Field label="SMTP host">
              <input type="text" value={form.smtpHost} onChange={set('smtpHost')} required style={inputStyle()} />
            </Field>
            <Field label="SMTP port">
              <input type="number" value={form.smtpPort} onChange={set('smtpPort')} required style={inputStyle()} />
            </Field>
            <Field label="SMTP username" hint="Optional — leave blank for an unauthenticated relay.">
              <input type="text" value={form.smtpUser} onChange={set('smtpUser')} style={inputStyle()} />
            </Field>
            <Field label="SMTP password" hint={isEdit ? 'Leave as *** (or clear) to keep the stored password.' : undefined}>
              <input type="password" value={form.smtpPass} onChange={set('smtpPass')} autoComplete="new-password" style={inputStyle()} />
            </Field>
            <Field label="From address">
              <input type="email" value={form.from} onChange={set('from')} required style={inputStyle()} />
            </Field>
            <Field label="Sender name" hint='Display name shown to recipients. Defaults to "AxiaOps" — change it to use your own team name.'>
              <input type="text" value={form.fromName} onChange={set('fromName')} style={inputStyle()} />
            </Field>
            <Field label="Recipients" hint="Comma-separated email addresses.">
              <input type="text" value={form.recipients} onChange={set('recipients')} required style={inputStyle()} />
            </Field>
          </>
        )}

        <Divider label="Trigger" />
        <Field label="Minimum monthly savings (USD)" hint="Don't notify unless a scan finds at least this much.">
          <input type="number" min="0" value={form.minSavings} onChange={set('minSavings')} style={inputStyle()} />
        </Field>
        <Field label="Digest size" hint="How many top services to list in the message.">
          <input type="number" min="0" value={form.digestTopN} onChange={set('digestTopN')} style={inputStyle()} />
        </Field>

        {error && <div style={{ color: 'var(--color-error)', fontSize: 12 }}>{error}</div>}

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
          <button type="button" onClick={onClose} style={ghostButton()}>Cancel</button>
          <button type="submit" disabled={submitDisabled} style={{ ...primaryButton(), opacity: submitDisabled ? 0.5 : 1, cursor: submitDisabled ? 'not-allowed' : 'pointer' }}>
            {mutation.isPending ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function initialForm(existing) {
  const cfg = existing?.config || {};
  const tr = existing?.trigger_rule || {};
  return {
    kind: existing?.kind || 'email',
    label: existing?.label || '',
    // slack / teams
    webhookUrl: cfg.webhook_url || '',
    // email — provider is a UI hint (prefills host/port); inferred from the
    // stored host in edit mode so the dropdown reflects reality.
    provider: inferEmailProvider(cfg.smtp_host),
    smtpHost: cfg.smtp_host || '',
    smtpPort: cfg.smtp_port != null ? String(cfg.smtp_port) : '587',
    smtpUser: cfg.smtp_user || '',
    smtpPass: cfg.smtp_pass || '', // arrives as *** in edit mode; preserved on save
    from: cfg.from || '',
    fromName: cfg.from_name || 'AxiaOps',
    recipients: Array.isArray(cfg.recipients) ? cfg.recipients.join(', ') : '',
    minSavings: tr.min_monthly_savings_usd != null ? String(tr.min_monthly_savings_usd) : '25',
    digestTopN: tr.digest_top_n != null ? String(tr.digest_top_n) : '10',
  };
}

function buildConfig(form) {
  if (form.kind === 'slack' || form.kind === 'teams') {
    return { webhook_url: form.webhookUrl };
  }
  return {
    smtp_host: form.smtpHost,
    smtp_port: Number(form.smtpPort) || 0,
    smtp_user: form.smtpUser,
    smtp_pass: form.smtpPass,
    from: form.from,
    from_name: form.fromName,
    recipients: form.recipients.split(',').map((s) => s.trim()).filter(Boolean),
  };
}

// ── deliveries modal ──────────────────────────────────────────────────────────

function DeliveriesModal({ channel, onClose, isDark }) {
  const q = useQuery({
    queryKey: ['channel-dispatches', channel.id],
    queryFn: () => listChannelDispatches(channel.id),
  });
  return (
    <ModalShell title={`Recent deliveries — ${channel.label}`} onClose={onClose} isDark={isDark}>
      {q.isPending ? (
        <div style={{ padding: 24, textAlign: 'center' }}><Spinner /></div>
      ) : q.isError ? (
        <div style={{ color: 'var(--color-error)', fontSize: 13 }}>Failed to load deliveries.</div>
      ) : (q.data || []).length === 0 ? (
        <div style={{ color: 'var(--color-text-muted)', fontSize: 13 }}>No deliveries yet — no scan has exceeded this channel&apos;s savings gate, or none has run. Use Test to verify the channel.</div>
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
          <thead>
            <tr style={{ borderBottom: `1px solid var(--color-border)` }}>
              <Th>When</Th><Th>Status</Th><Th>Detail</Th>
            </tr>
          </thead>
          <tbody>
            {(q.data || []).map((d) => (
              <tr key={d.id} style={{ borderBottom: `1px solid var(--color-border)` }}>
                <Td>{fmtTime(d.dispatched_at || d.created_at)}</Td>
                <Td><DispatchStatus status={d.status} /></Td>
                <Td style={{ color: 'var(--color-text-muted)' }}>{d.error || (d.source === 'test' ? 'Test send' : `${d.zombie_count ?? 0} zombies`)}</Td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </ModalShell>
  );
}

function fmtTime(s) {
  if (!s) return '—';
  const d = new Date(s);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString();
}

// ── shared presentational helpers (mirrors the SSO Connections pane) ───────────

function EmptyState() {
  return (
    <div style={{ padding: 32, textAlign: 'center', color: 'var(--color-text-muted)', fontSize: 13 }}>
      No notification channels yet. Click <strong>Add channel</strong> to send scan digests to email, Slack, or Teams.
    </div>
  );
}

function DispatchStatus({ status }) {
  const fg = {
    sent: '#10b981',
    failed: '#ef4444',
    skipped_threshold: 'var(--color-text-muted)',
    queued: 'var(--color-text-muted)',
  }[status] || 'var(--color-text-muted)';
  return <span style={{ fontSize: 11, fontWeight: 600, color: fg }}>{status}</span>;
}

function Th({ children }) {
  return <th style={{ padding: '10px 12px', textAlign: 'left', fontWeight: 600, fontSize: 12, color: 'var(--color-text-muted)', letterSpacing: 0.3 }}>{children}</th>;
}
function Td({ children, style }) {
  return <td style={{ padding: '10px 12px', color: 'var(--color-text)', ...style }}>{children}</td>;
}
function Banner({ children, color, bg }) {
  return <div style={{ padding: '8px 12px', marginBottom: 12, borderRadius: 6, color, backgroundColor: bg, fontSize: 13 }}>{children}</div>;
}
function ModalShell({ children, onClose, lockClose, isDark, title }) {
  const handleBackdropClick = lockClose ? undefined : onClose;
  return (
    <div onClick={handleBackdropClick} style={{ position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.5)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 520, maxWidth: '90vw', maxHeight: '90vh', overflowY: 'auto', backgroundColor: isDark ? '#1f2937' : '#fff', color: 'var(--color-text)', borderRadius: 8, padding: 20, boxShadow: '0 20px 50px rgba(0,0,0,0.3)' }}>
        <h2 style={{ margin: 0, marginBottom: 16, fontSize: 16, fontWeight: 700 }}>{title}</h2>
        {children}
      </div>
    </div>
  );
}
function Field({ label, hint, children }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12 }}>
      <span style={{ fontWeight: 600 }}>{label}</span>
      {children}
      {hint && <span style={{ fontSize: 11, opacity: 0.7 }}>{hint}</span>}
    </label>
  );
}
function Divider({ label }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, margin: '4px 0' }}>
      <span style={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--color-text-muted)' }}>{label}</span>
      <div style={{ flex: 1, height: 1, backgroundColor: 'var(--color-border)' }} />
    </div>
  );
}
function inputStyle() {
  return { padding: '6px 10px', border: `1px solid var(--color-border)`, borderRadius: 6, fontSize: 13, backgroundColor: 'var(--color-bg)', color: 'var(--color-text)', width: '100%', boxSizing: 'border-box' };
}
function humanize(err, fallback) {
  if (!err) return fallback;
  const detail = parseAPIError(err);
  if (err.status === 400) return detail || 'Invalid request — check the fields.';
  if (err.status === 403) return 'You do not have permission to manage channels.';
  if (err.status === 404) return 'Channel no longer exists.';
  if (err.status >= 500) return detail || 'Server error — please try again.';
  return err.message || fallback;
}
function parseAPIError(err) {
  if (!err?.body) return '';
  try {
    const parsed = JSON.parse(err.body);
    return parsed.error || parsed.message || '';
  } catch {
    return err.body;
  }
}
function sanitizeForDialog(s) {
  let out = '';
  for (const ch of String(s ?? '')) {
    const cp = ch.codePointAt(0);
    const drop =
      cp < 0x20 || cp === 0x7f || (cp >= 0x80 && cp <= 0x9f) ||
      cp === 0x200e || cp === 0x200f || (cp >= 0x202a && cp <= 0x202e) ||
      cp === 0x2028 || cp === 0x2029;
    if (drop) continue;
    out += ch;
    if (out.length >= 80) break;
  }
  return out;
}
function primaryButton() {
  return { padding: '7px 14px', border: 'none', borderRadius: 6, backgroundColor: 'var(--color-accent)', color: 'var(--color-text-on-dark)', fontWeight: 600, fontSize: 13, cursor: 'pointer' };
}
function ghostButton() {
  return { padding: '5px 10px', border: `1px solid var(--color-border)`, borderRadius: 6, backgroundColor: 'transparent', color: 'var(--color-text)', fontSize: 12, fontWeight: 600, cursor: 'pointer' };
}
