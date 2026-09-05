import { useState } from 'react';
import { useQueryClient, useMutation } from '@tanstack/react-query';
import { updateAccount, deleteAccount, scanAccount } from '../api/client';
import { useToast } from '../context/ToastContext';
import { useScanStatus } from '../hooks/useScanStatus';
import { Spinner } from '../components/primitives';
import { useDestructiveConfirm, DestructiveConfirmModal } from '../components/DestructiveConfirm';
import { BillingSourceConfig, roleNameFromArn } from './ConnectScreen';

function Field({ label, value, onChange, placeholder, mono, type = 'text', hint, readOnly }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text-mid)' }}>{label}</label>
      <input
        style={{
          width: '100%',
          boxSizing: 'border-box',
          backgroundColor: readOnly ? 'var(--color-surface)' : 'var(--color-surface-alt)',
          border: `1px solid var(--color-border)`,
          borderRadius: 8,
          padding: '10px 12px',
          fontSize: 14,
          color: readOnly ? 'var(--color-text-mid)' : 'var(--color-text)',
          fontFamily: mono ? '"Geist Mono Variable", monospace' : undefined,
          cursor: readOnly ? 'default' : 'text',
        }}
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        autoCapitalize="none"
        autoCorrect="off"
        type={type}
        readOnly={readOnly}
      />
      {hint && <span style={{ fontSize: 12, color: 'var(--color-text-muted)', fontStyle: 'italic' }}>{hint}</span>}
    </div>
  );
}

function StatusBadge({ status }) {
  // Inline dot + label — no pill chrome. The colored dot is the at-a-glance
  // indicator; the headline beside it carries the operational message.
  const config = {
    connected:            { color: 'var(--color-success)', label: 'Connected' },
    error:                { color: 'var(--color-error)',   label: 'Disconnected' },
    scan_timeout:         { color: 'var(--color-warning)', label: 'Timed Out' },
    circuit_breaker_open: { color: 'var(--color-warning)', label: 'Paused' },
    scanning:             { color: 'var(--color-accent)',  label: 'Scanning…' },
    pending_cur_delivery: { color: 'var(--color-text-muted)', label: 'Awaiting First Delivery' },
  };
  const c = config[status] ?? { color: 'var(--color-text-muted)', label: 'Unknown' };

  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
      <div style={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: c.color, flexShrink: 0 }} />
      <span style={{ fontSize: 13, fontWeight: 700, color: c.color }}>{c.label}</span>
    </div>
  );
}

// One-line affirmation/diagnosis paired with the chip. Mirrors the License
// page pattern (chip = state, sentence = consequence) so screen-reader users
// hear the meaning, not just the state name.
function statusHeadline(status) {
  switch (status) {
    case 'connected':            return 'Connection healthy. Scans run on schedule.';
    case 'error':                return 'Last scan failed — check credentials and region.';
    case 'scan_timeout':         return 'Last scan timed out at 15 minutes.';
    case 'circuit_breaker_open': return 'Paused after repeated scan failures.';
    case 'scanning':             return 'Scan in progress.';
    case 'pending_cur_delivery': return 'Billing export provisioned — first cost data typically arrives within 24 hours.';
    default:                     return '';
  }
}

export default function AccountSettingsScreen({ account, onBack, onAccountUpdated, onAccountDeleted }) {
  const { toast }   = useToast();
  const { watch }   = useScanStatus();
  const queryClient = useQueryClient();

  // Role accounts have no access_key_id; access_key accounts have no role_arn.
  // Render-time branching on auth_method keeps each mode's fields cleanly
  // separated and avoids the validation crash where the access-key validator
  // rejected a perfectly valid role-mode submission.
  const isRoleMode = account?.auth_method === 'role';

  const [label, setLabel]             = useState(account?.label ?? '');
  const [accessKeyId, setAccessKeyId] = useState(account?.access_key_id ?? '');
  const [secretKey, setSecretKey]     = useState('');
  const [region, setRegion]           = useState(account?.region ?? 'eu-central-1');
  const [scanIntervalHours, setScanIntervalHours] = useState(account?.scan_interval_hours?.toString() ?? '24');
  const [billingSource, setBillingSource] = useState(account?.billing_source === 'cur_athena' ? 'cur_athena' : 'cost_explorer');
  const [curConfig, setCurConfig] = useState({
    cur_database: account?.cur_database || 'axiaops_cur_db',
    cur_table: account?.cur_table || 'axiaops_cur_table',
    cur_workgroup: account?.cur_workgroup || 'axiaops_athena_wg',
    cur_results_s3: account?.cur_results_s3 || '',
    cur_region: account?.cur_region || 'us-east-1',
    role_name: roleNameFromArn(account?.role_arn) || 'AxiaOpsRole',
  });
  const [loading, setLoading]         = useState(false);
  const [error, setError]             = useState('');
  const scanning = account?.status === 'scanning';

  // accountName fallback: prefer label, then for access-key accounts a slice
  // of the key id, for role accounts the trailing segment of the role ARN.
  // The previous code crashed on role accounts because access_key_id was
  // empty / undefined and `.slice(0,8)` returned '' (or threw).
  function deriveAccountName(a) {
    if (!a) return '';
    if (a.label) return a.label;
    if (a.access_key_id) return a.access_key_id.slice(0, 8) + '…';
    if (a.role_arn) {
      const parts = a.role_arn.split('/');
      return parts[parts.length - 1] || a.role_arn;
    }
    return 'AWS account';
  }
  const accountName = deriveAccountName(account);

  // Type-to-confirm delete flow — same UX as Profile / Organization
  // destructive flows. The user must type the account name before the
  // Delete button enables.
  const deleteCtrl = useDestructiveConfirm({
    target: accountName,
    mutationFn: () => deleteAccount(account.id),
    successMessage: 'Account deleted.',
    onSuccess: () => onAccountDeleted(account.id),
    toast,
  });

  async function handleSave() {
    // Validation diverges by auth mode: access-key accounts must always carry
    // their access key id (secret is optional — blank means keep existing).
    // Role accounts have no editable credentials in this screen (the role ARN
    // change flow lives behind the dedicated Connect → Role tab on
    // ConnectScreen, which re-runs the verify probe end-to-end).
    if (!isRoleMode && !accessKeyId.trim()) { setError('Access Key ID is required.'); return; }
    const scanInterval = parseInt(scanIntervalHours, 10);
    if (isNaN(scanInterval) || scanInterval < 0) { setError('Scan interval must be a number ≥ 0.'); return; }
    setError('');
    setLoading(true);
    try {
      const payload = {
        label: label.trim() || 'My AWS Account',
        region: region.trim() || 'eu-central-1',
        scan_interval_hours: scanInterval,
      };
      // Only thread credential fields for access-key accounts. Sending an
      // empty accessKeyId on a role account would write garbage into the
      // PATCH /v1/accounts/{id} body and confuse the api's account writer.
      if (!isRoleMode) {
        payload.accessKeyId = accessKeyId.trim();
        payload.secretKey = secretKey.trim() || undefined;
      }
      if (billingSource === 'cur_athena') {
        Object.assign(payload, {
          billing_source: 'cur_athena',
          cur_database: curConfig.cur_database || 'axiaops_cur_db',
          cur_table: curConfig.cur_table || 'axiaops_cur_table',
          cur_workgroup: curConfig.cur_workgroup || 'axiaops_athena_wg',
          cur_results_s3: curConfig.cur_results_s3 || `s3://axiaops-athena-results-${account.account_id}-${curConfig.cur_region || 'us-east-1'}`,
          cur_region: curConfig.cur_region || 'us-east-1',
        });
      } else {
        Object.assign(payload, { billing_source: 'cost_explorer' });
      }
      const result = await updateAccount(account.id, payload);
      toast('Account settings saved', 'success');
      onAccountUpdated(result);
    } catch {
      setError(isRoleMode
        ? 'Failed to update. Check the label, region, and scan interval.'
        : 'Failed to update. Check your credentials and try again.');
    } finally {
      setLoading(false);
    }
  }

  const scanMutation = useMutation({
    mutationFn: scanAccount,
    onMutate: async (accountId) => {
      await queryClient.cancelQueries({ queryKey: ['accounts'] });
      const previous = queryClient.getQueryData(['accounts']);
      const displayLabel = account.label ?? accountId.slice(0, 8);

      queryClient.setQueryData(['accounts'], (accs = []) =>
        accs.map(a => a.id === accountId ? { ...a, status: 'scanning' } : a),
      );
      toast(`Starting scan for ${displayLabel}…`, 'info');
      return { previous, displayLabel };
    },
    onError: (err, accountId, ctx) => {
      if (err?.code === 'already_scanning') {
        toast(`Scan already running for ${ctx?.displayLabel ?? 'account'}`, 'info');
        watch(accountId, { label: account.label, onEnd: () => onAccountUpdated(account) });
        return;
      }
      if (ctx?.previous) queryClient.setQueryData(['accounts'], ctx.previous);
      toast(`Couldn't start scan for ${ctx?.displayLabel ?? 'account'}`, 'error');
    },
    onSuccess: (_data, accountId) => {
      watch(accountId, { label: account.label, onEnd: () => onAccountUpdated(account) });
    },
  });

  const handleScan = () => scanMutation.mutate(account.id);


  return (
    <div style={{ minHeight: '100%', backgroundColor: 'var(--color-bg)' }}>
      <div style={{ maxWidth: 560, margin: '0 auto', padding: '0 0 64px' }}>

        {/* Header */}
        <div style={{ padding: '20px 20px 16px', borderBottom: `1px solid var(--color-border)` }}>
          <button onClick={onBack} style={{ padding: '4px 0', background: 'none', border: 'none', cursor: 'pointer', marginBottom: 12 }}>
            <span style={{ color: 'var(--color-accent)', fontSize: 14, fontWeight: 600 }}>← Back</span>
          </button>
          <h1 style={{ fontSize: 20, fontWeight: 800, color: 'var(--color-text)', margin: '0 0 4px' }}>{accountName}</h1>
          <p style={{ fontSize: 13, color: 'var(--color-text-muted)', margin: 0 }}>{account.region}</p>
        </div>

        <div style={{ padding: '20px' }}>
          {/* Status + quick actions */}
          <div style={{
            backgroundColor: 'var(--color-surface)',
            border: `1px solid var(--color-border)`,
            borderRadius: 12,
            padding: 20,
            marginBottom: 16,
            display: 'flex',
            flexDirection: 'column',
            gap: 14,
          }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 10 }}>
                <StatusBadge status={account.status} />
                <span style={{ fontSize: 13, color: 'var(--color-text)', fontWeight: 600 }}>
                  {statusHeadline(account.status)}
                </span>
              </div>
              {account.last_scanned_at && (
                <span style={{ fontSize: 12, color: 'var(--color-text-muted)' }}>
                  Last scan: {new Date(account.last_scanned_at).toLocaleString()}
                </span>
              )}
            </div>

            {account.status === 'circuit_breaker_open' && (
              <div style={{ backgroundColor: `var(--color-warning)18`, border: `1px solid var(--color-warning)40`, borderRadius: 8, padding: '10px 12px' }}>
                <span style={{ fontSize: 13, color: 'var(--color-warning)', lineHeight: '20px', display: 'block' }}>
                  Too many consecutive scan failures. Wait a few minutes before retrying, or check your IAM credentials.
                </span>
              </div>
            )}

            {/* Actions row */}
            <div style={{ display: 'flex', gap: 10 }}>
              <button
                onClick={handleScan}
                disabled={scanning || account.status === 'circuit_breaker_open'}
                aria-label="Run scan now"
                style={{
                  flex: 1,
                  padding: '11px',
                  borderRadius: 8,
                  backgroundColor: 'var(--color-accent)',
                  border: 'none',
                  cursor: scanning || account.status === 'circuit_breaker_open' ? 'not-allowed' : 'pointer',
                  opacity: scanning || account.status === 'circuit_breaker_open' ? 0.6 : 1,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: 6,
                }}
              >
                {scanning ? <Spinner size={16} color={'var(--color-text-on-dark)'} /> : (
                  <>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="23 4 23 10 17 10" /><polyline points="1 20 1 14 7 14" />
                      <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
                    </svg>
                    <span style={{ color: 'var(--color-text-on-dark)', fontSize: 14, fontWeight: 700 }}>Scan Now</span>
                  </>
                )}
              </button>
              {/* Destructive primary — filled red, white label. Mirrors the
                  Delete Organization button (DangerSection in Profile.jsx /
                  Organization.jsx) so destructive primaries look the same
                  across the app. Type-to-confirm modal still gates the action. */}
              <button
                onClick={deleteCtrl.openModal}
                disabled={deleteCtrl.isPending}
                aria-label="Delete account"
                style={{
                  padding: '11px 16px',
                  borderRadius: 8,
                  backgroundColor: 'var(--color-error)',
                  border: 'none',
                  cursor: deleteCtrl.isPending ? 'not-allowed' : 'pointer',
                  opacity: deleteCtrl.isPending ? 0.6 : 1,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: 6,
                }}
              >
                {deleteCtrl.isPending ? <Spinner size={16} color={'var(--color-text-on-dark)'} /> : (
                  <span style={{ color: 'var(--color-text-on-dark)', fontSize: 14, fontWeight: 700 }}>Delete</span>
                )}
              </button>
            </div>
          </div>

          {/* Edit form */}
          <div style={{
            backgroundColor: 'var(--color-surface)',
            border: `1px solid var(--color-border)`,
            borderRadius: 12,
            padding: 20,
            display: 'flex',
            flexDirection: 'column',
            gap: 18,
          }}>
            <span style={{ fontSize: 15, fontWeight: 700, color: 'var(--color-text)' }}>Account Details</span>

            <Field label="Label" value={label} onChange={setLabel} placeholder="e.g. Production AWS" />

            {isRoleMode ? (
              <>
                <Field label="Role ARN" value={account?.role_arn ?? ''} onChange={() => {}} mono readOnly hint="Edit via Connect → Role-based to re-verify the trust policy." />
                <Field label="External ID" value={account?.external_id ?? ''} onChange={() => {}} mono readOnly />
              </>
            ) : (
              <>
                <Field label="AWS Access Key ID" value={accessKeyId} onChange={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono />
                <Field label="AWS Secret Access Key" value={secretKey} onChange={setSecretKey} placeholder="Leave blank to keep existing" mono type="password" />
              </>
            )}

            <Field label="Region" value={region} onChange={setRegion} placeholder="eu-central-1" mono />
            <Field
              label="Auto-scan interval (hours)"
              value={scanIntervalHours}
              onChange={setScanIntervalHours}
              placeholder="24"
              type="number"
              hint="0 = on-demand only, or enter hours between automatic scans"
            />

            {/* Folded by default on this edit screen — most edits here are
                label/region/interval tweaks that have nothing to do with
                billing source. savedValues shows the account's real current
                CUR config while folded (instead of the generic new-connection
                sentence, which is wrong for an account like a manually-named
                test setup), and the fields are pre-filled with those same
                values the moment Advanced Configuration is opened — nothing
                is actually hidden, just collapsed. */}
            <BillingSourceConfig
              billingSource={billingSource}
              setBillingSource={setBillingSource}
              curConfig={curConfig}
              setCurConfig={setCurConfig}
              savedValues={account?.cur_database ? {
                cur_database: account.cur_database,
                cur_table: account.cur_table,
                cur_workgroup: account.cur_workgroup,
                cur_results_s3: account.cur_results_s3,
                cur_region: account.cur_region,
              } : null}
              accountId={account?.id}
            />

            {error && (
              <div style={{ backgroundColor: `var(--color-error)18`, border: `1px solid var(--color-error)40`, borderRadius: 8, padding: '10px 12px' }}>
                <span style={{ fontSize: 13, color: 'var(--color-error)', fontWeight: 500 }}>{error}</span>
              </div>
            )}

            <button
              onClick={handleSave}
              disabled={loading}
              style={{
                backgroundColor: 'var(--color-accent)',
                borderRadius: 10,
                padding: '14px',
                border: 'none',
                cursor: loading ? 'not-allowed' : 'pointer',
                opacity: loading ? 0.65 : 1,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              {loading ? <Spinner size={20} color={'var(--color-text-on-dark)'} /> : <span style={{ color: 'var(--color-text-on-dark)', fontSize: 15, fontWeight: 700 }}>Save Changes</span>}
            </button>
          </div>
        </div>
      </div>

      <DestructiveConfirmModal
        ctrl={deleteCtrl}
        title="Delete Account?"
        warning={`This permanently deletes the cloud account "${accountName}", all its scan history, and every resource record tied to it. Cannot be undone.`}
        targetLabel="account name"
        confirmLabel="Delete Account"
      />
    </div>
  );
}
