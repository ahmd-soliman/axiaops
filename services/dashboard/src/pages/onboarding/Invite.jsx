import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTheme } from '../../theme/ThemeContext';
import { useToast } from '../../context/ToastContext';
import { createInvitation } from '../../api/client';

const ROLES = [
  { value: 'viewer', label: 'Viewer — read-only' },
  { value: 'member', label: 'Member — manage accounts and zombies' },
  { value: 'admin', label: 'Admin — full control except ownership' },
];

// Step 2 of 3 — invite teammates. Skippable. Each row is sent as a separate
// POST /v1/invitations; failures are toasted but don't block advancement.
// See docs/onboarding-wizard.md §8.3.
export default function OnboardingInvite() {
  const { theme: t, isDark } = useTheme();
  const navigate = useNavigate();
  const { toast } = useToast();

  const [rows, setRows] = useState([{ email: '', role: 'member' }]);
  const [sending, setSending] = useState(false);

  const border = isDark ? 'rgba(255,255,255,0.12)' : '#e5e7eb';
  const inputBg = isDark ? 'rgba(0,0,0,0.2)' : '#fff';

  function updateRow(i, patch) {
    setRows(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  }
  function addRow() {
    setRows([...rows, { email: '', role: 'member' }]);
  }
  function removeRow(i) {
    setRows(rows.filter((_, idx) => idx !== i));
  }

  async function sendAll() {
    const valid = rows.filter((r) => r.email.trim() !== '');
    if (valid.length === 0) {
      navigate('/onboarding/aws-account');
      return;
    }
    setSending(true);
    let succeeded = 0;
    for (const row of valid) {
      try {
        await createInvitation(row.email.trim(), row.role);
        succeeded += 1;
      } catch (err) {
        const code = err?.body?.error;
        const msg = err?.body?.message || `Could not invite ${row.email}.`;
        if (code === 'already_a_member') {
          toast({ kind: 'info', message: `${row.email} is already a member.` });
        } else if (code === 'user_exists_use_memberships') {
          toast({ kind: 'info', message: `${row.email} already has an account — add them from Settings → Team.` });
        } else {
          toast({ kind: 'error', message: msg });
        }
      }
    }
    if (succeeded > 0) {
      toast({ kind: 'success', message: `Invitations sent to ${succeeded} teammate${succeeded === 1 ? '' : 's'}.` });
    }
    setSending(false);
    navigate('/onboarding/aws-account');
  }

  function skip() {
    navigate('/onboarding/aws-account');
  }

  return (
    <div>
      <h1 style={{ color: t.text, fontSize: 26, fontWeight: 700, margin: 0, marginBottom: 8 }}>
        Invite your teammates
      </h1>
      <p style={{ color: t.textMid, fontSize: 14, marginTop: 0, marginBottom: 24 }}>
        Send email invitations now, or skip and do it later from Settings → Team.
      </p>

      {rows.map((row, i) => (
        <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 10 }}>
          <input
            type="email"
            value={row.email}
            onChange={(e) => updateRow(i, { email: e.target.value })}
            placeholder="teammate@example.com"
            style={{
              flex: 2,
              padding: '8px 10px',
              border: `1px solid ${border}`,
              borderRadius: 6,
              backgroundColor: inputBg,
              color: t.text,
              fontSize: 13,
            }}
          />
          <select
            value={row.role}
            onChange={(e) => updateRow(i, { role: e.target.value })}
            style={{
              flex: 1,
              padding: '8px 10px',
              border: `1px solid ${border}`,
              borderRadius: 6,
              backgroundColor: inputBg,
              color: t.text,
              fontSize: 13,
            }}
          >
            {ROLES.map((r) => (
              <option key={r.value} value={r.value}>{r.label}</option>
            ))}
          </select>
          {rows.length > 1 && (
            <button
              type="button"
              onClick={() => removeRow(i)}
              style={{
                padding: '0 10px',
                border: `1px solid ${border}`,
                borderRadius: 6,
                backgroundColor: 'transparent',
                color: t.textMuted,
                cursor: 'pointer',
                fontSize: 16,
              }}
              aria-label="Remove invitation row"
            >
              ×
            </button>
          )}
        </div>
      ))}

      <button
        type="button"
        onClick={addRow}
        style={{
          padding: '6px 12px',
          border: `1px dashed ${border}`,
          borderRadius: 6,
          backgroundColor: 'transparent',
          color: t.textMid,
          fontSize: 12,
          cursor: 'pointer',
          marginBottom: 24,
        }}
      >
        + Add another
      </button>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <button
          type="button"
          onClick={skip}
          disabled={sending}
          style={{
            padding: '10px 16px',
            border: 'none',
            borderRadius: 8,
            backgroundColor: 'transparent',
            color: t.textMuted,
            fontSize: 14,
            cursor: 'pointer',
          }}
        >
          Skip for now
        </button>
        <button
          type="button"
          onClick={sendAll}
          disabled={sending}
          style={{
            padding: '10px 20px',
            border: 'none',
            borderRadius: 8,
            backgroundColor: t.accent,
            color: '#fff',
            fontWeight: 600,
            fontSize: 14,
            cursor: sending ? 'not-allowed' : 'pointer',
            opacity: sending ? 0.5 : 1,
          }}
        >
          {sending ? 'Sending…' : 'Send invitations'}
        </button>
      </div>
    </div>
  );
}
