import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTheme } from '../../../theme/ThemeContext';
import {
  listSSOConnections,
  listSSOGroupMappings,
  replaceSSOGroupMappings,
} from '../../../api/client';
import { Spinner } from '../../../components/primitives';

// Group Mappings pane: per-connection editable table mapping IdP group
// identifiers to AxiaOps roles. PUT replaces the full set in one
// transaction (handler.replaceGroupMappings) — there's no partial update,
// so the UI keeps everything in local state and only persists on Save.
//
// Roles intentionally exclude `owner`: the JIT resolver maps the highest
// matching mapping to a role, and owner is sticky (transfer/bootstrap only).
// The DB CHECK constraint on sso_group_mappings.role enforces this at the
// write boundary too — keeping the UI list aligned avoids 400s on save.

const ASSIGNABLE_ROLES = ['viewer', 'member', 'admin'];

export default function GroupMappings() {
  const { isDark } = useTheme();
  const qc = useQueryClient();

  const conns = useQuery({ queryKey: ['sso-connections'], queryFn: listSSOConnections });
  const [connectionId, setConnectionId] = useState('');

  // Default to first connection once loaded.
  useEffect(() => {
    if (!connectionId && (conns.data || []).length > 0) {
      setConnectionId(conns.data[0].id);
    }
  }, [conns.data, connectionId]);

  return (
    <div>
      <div style={{ marginBottom: 16, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--color-text-mid)' }}>
          Connection:
          <select
            value={connectionId}
            onChange={(e) => setConnectionId(e.target.value)}
            disabled={conns.isPending || (conns.data || []).length === 0}
            style={{ ...inputStyle(), width: 'auto', minWidth: 220 }}
          >
            {(conns.data || []).map((c) => (
              <option key={c.id} value={c.id}>{c.label} ({c.protocol})</option>
            ))}
          </select>
        </label>
      </div>

      {conns.isPending ? (
        <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
      ) : (conns.data || []).length === 0 ? (
        <EmptyState message="Create a connection first — group mappings are scoped per connection." />
      ) : connectionId ? (
        <Editor
          key={connectionId}
          connectionId={connectionId}
          isDark={isDark}
          onSaved={() => qc.invalidateQueries({ queryKey: ['sso-group-mappings', connectionId] })}
        />
      ) : null}
    </div>
  );
}

function Editor({ connectionId, isDark, onSaved }) {
  const mappings = useQuery({
    queryKey: ['sso-group-mappings', connectionId],
    queryFn: () => listSSOGroupMappings(connectionId),
    enabled: !!connectionId,
  });

  const [rows, setRows] = useState([]);
  const [error, setError] = useState('');
  const [savedTick, setSavedTick] = useState(false);
  const hydratedRef = useRef(false);

  // Hydrate local state from the server response ONCE per mount. Each row gets
  // a stable local key so React doesn't reorder inputs on edit; existing rows
  // reuse their server ID, new rows mint a uuid-ish.
  //
  // Hydrate-once (not on every mappings.data change): after a save, onSaved
  // invalidates the query and the background refetch returns a fresh array
  // reference. Re-running this on that reference would overwrite any edits the
  // user typed between pressing Save and the refetch resolving. The Editor is
  // keyed by connectionId upstream, so switching connections remounts it and
  // re-hydrates from the new connection's data.
  useEffect(() => {
    if (hydratedRef.current || !mappings.data) return;
    hydratedRef.current = true;
    setRows((mappings.data || []).map((m) => ({
      _key:               m.id,
      group_external_id:  m.group_external_id || '',
      group_display_name: m.group_display_name || '',
      role:               m.role,
    })));
  }, [mappings.data]);

  const replaceMutation = useMutation({
    mutationFn: () => {
      const cleaned = rows
        .map((r) => ({
          group_external_id:  r.group_external_id.trim(),
          group_display_name: r.group_display_name.trim(),
          role:               r.role,
        }))
        .filter((r) => r.group_external_id !== '');
      return replaceSSOGroupMappings(connectionId, cleaned);
    },
    onSuccess: () => {
      setError('');
      setSavedTick(true);
      setTimeout(() => setSavedTick(false), 1800);
      onSaved();
    },
    onError: (err) => setError(humanize(err, 'Save failed')),
  });

  const dirty = useMemo(() => {
    // PUT replaces the full set order-insensitively, so the comparison
    // must be order-insensitive too — comparing positionally would flag
    // pure reorders as dirty (false positive) and could miss content
    // changes if rows are deleted in the middle (false negative).
    // Serialise each side as a sorted-by-external-id signature and
    // string-compare.
    // JSON-stringify per row so embedded spaces/pipes in user-supplied
    // group names can't make distinct rows look identical.
    const sig = (list) => list
      .map((m) => JSON.stringify([m.group_external_id, m.group_display_name, m.role]))
      .sort()
      .join('|');
    const originalSig = sig((mappings.data || []).map((m) => ({
      group_external_id:  m.group_external_id || '',
      group_display_name: m.group_display_name || '',
      role:               m.role,
    })));
    const currentSig = sig(rows
      .map((r) => ({
        group_external_id:  r.group_external_id.trim(),
        group_display_name: r.group_display_name.trim(),
        role:               r.role,
      }))
      .filter((r) => r.group_external_id !== ''));
    return originalSig !== currentSig;
  }, [mappings.data, rows]);

  const addRow = () => setRows((rs) => [...rs, {
    _key: `new-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
    group_external_id: '',
    group_display_name: '',
    role: 'member',
  }]);

  const updateRow = (idx, key, value) =>
    setRows((rs) => rs.map((r, i) => (i === idx ? { ...r, [key]: value } : r)));

  const removeRow = (idx) => setRows((rs) => rs.filter((_, i) => i !== idx));

  if (mappings.isPending) {
    return <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>;
  }
  if (mappings.isError) {
    return <div style={{ padding: 24, color: 'var(--color-error)' }}>Failed to load mappings.</div>;
  }

  return (
    <div>
      <p style={{ margin: 0, marginBottom: 12, fontSize: 13, color: 'var(--color-text-mid)' }}>
        Map IdP groups to AxiaOps roles. Highest matching role wins; users in no mapped group fall through to the connection's default role.
      </p>

      {error && (
        <Banner color={'var(--color-error)'} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>
          {error}
        </Banner>
      )}
      {savedTick && (
        <Banner color="#10b981" bg={isDark ? 'rgba(16,185,129,0.15)' : '#d1fae5'}>
          Saved.
        </Banner>
      )}

      {rows.length === 0 ? (
        <EmptyState message="No mappings yet. Click Add row to start mapping groups to roles." />
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: `1px solid var(--color-border)` }}>
              <Th>Group identifier</Th>
              <Th>Display name</Th>
              <Th>Role</Th>
              <Th></Th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r, idx) => (
              <tr key={r._key} style={{ borderBottom: `1px solid var(--color-border)` }}>
                <Td>
                  <input
                    type="text"
                    value={r.group_external_id}
                    onChange={(e) => updateRow(idx, 'group_external_id', e.target.value)}
                    placeholder="objectId, group name, etc."
                    style={inputStyle()}
                  />
                </Td>
                <Td>
                  <input
                    type="text"
                    value={r.group_display_name}
                    onChange={(e) => updateRow(idx, 'group_display_name', e.target.value)}
                    placeholder="optional"
                    style={inputStyle()}
                  />
                </Td>
                <Td>
                  <select
                    value={r.role}
                    onChange={(e) => updateRow(idx, 'role', e.target.value)}
                    style={inputStyle()}
                  >
                    {ASSIGNABLE_ROLES.map((role) => (
                      <option key={role} value={role}>{role}</option>
                    ))}
                  </select>
                </Td>
                <Td>
                  <button type="button" onClick={() => removeRow(idx)} style={{ ...ghostButton(), color: 'var(--color-error)' }}>
                    Remove
                  </button>
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 16 }}>
        <button type="button" onClick={addRow} style={ghostButton()}>+ Add row</button>
        <button
          type="button"
          onClick={() => replaceMutation.mutate()}
          disabled={!dirty || replaceMutation.isPending}
          style={{
            ...primaryButton(),
            opacity: !dirty || replaceMutation.isPending ? 0.5 : 1,
            cursor: !dirty || replaceMutation.isPending ? 'not-allowed' : 'pointer',
          }}
        >
          {replaceMutation.isPending ? 'Saving…' : 'Save mappings'}
        </button>
      </div>
    </div>
  );
}

function EmptyState({ message }) {
  return (
    <div style={{ padding: 32, textAlign: 'center', color: 'var(--color-text-muted)', fontSize: 13 }}>
      {message}
    </div>
  );
}

function Th({ children }) {
  return (
    <th style={{ padding: '10px 12px', textAlign: 'left', fontWeight: 600, fontSize: 12, color: 'var(--color-text-muted)', letterSpacing: 0.3 }}>
      {children}
    </th>
  );
}

function Td({ children }) {
  return <td style={{ padding: '8px 12px', color: 'var(--color-text)', verticalAlign: 'middle' }}>{children}</td>;
}

function Banner({ children, color, bg }) {
  return (
    <div style={{ padding: '8px 12px', marginBottom: 12, borderRadius: 6, color, backgroundColor: bg, fontSize: 13 }}>
      {children}
    </div>
  );
}

function inputStyle() {
  return {
    padding: '6px 10px',
    border: `1px solid var(--color-border)`,
    borderRadius: 6,
    fontSize: 13,
    backgroundColor: 'var(--color-bg)',
    color: 'var(--color-text)',
    width: '100%',
    boxSizing: 'border-box',
  };
}

function primaryButton() {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: 'var(--color-accent)',
    color: 'var(--color-text-on-dark)',
    fontWeight: 600,
    fontSize: 13,
    cursor: 'pointer',
  };
}

function ghostButton() {
  return {
    padding: '5px 10px',
    border: `1px solid var(--color-border)`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: 'var(--color-text)',
    fontSize: 12,
    fontWeight: 600,
    cursor: 'pointer',
  };
}

function humanize(err, fallback) {
  if (!err) return fallback;
  const detail = parseAPIError(err);
  if (err.status === 400) return detail || 'Invalid mapping — check the role values.';
  if (err.status === 403) return 'You do not have permission to manage SSO.';
  if (err.status === 404) return 'Connection no longer exists.';
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
