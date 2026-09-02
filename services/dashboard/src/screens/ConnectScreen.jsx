import { useState } from 'react';
import { connectAccount, updateAccount, draftAccount, verifyAccount } from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { Spinner } from '../components/primitives';
import { AXIAOPS_AWS_ACCOUNT_ID, AXIAOPS_CFN_TEMPLATE_URL } from '../config';

function Field({ label, value, onChange, placeholder, mono, type = 'text', hint, readOnly }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text-mid)' }}>
        {label}
      </label>
      <input
        style={{
          width: '100%',
          boxSizing: 'border-box',
          backgroundColor: 'var(--color-surface-alt)',
          border: `1px solid var(--color-border)`,
          borderRadius: 8,
          padding: '10px 12px',
          fontSize: 14,
          color: 'var(--color-text)',
          fontFamily: mono ? '"Geist Mono Variable", monospace' : undefined,
        }}
        value={value}
        onChange={e => onChange?.(e.target.value)}
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

function CopyableBlock({ label, value }) {
  const [copied, setCopied] = useState(false);
  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch (_) { /* clipboard write may be blocked */ }
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <label style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text-mid)' }}>{label}</label>
        <button
          onClick={handleCopy}
          style={{ background: 'none', border: `1px solid var(--color-border)`, color: 'var(--color-text-mid)', fontSize: 12, padding: '4px 10px', borderRadius: 6, cursor: 'pointer' }}
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <code style={{
        display: 'block',
        backgroundColor: 'var(--color-surface-alt)',
        border: `1px solid var(--color-border)`,
        borderRadius: 8,
        padding: '10px 12px',
        fontSize: 13,
        fontFamily: '"Geist Mono Variable", monospace',
        color: 'var(--color-text)',
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-all',
      }}>{value}</code>
    </div>
  );
}

// Build the customer's trust policy JSON with the AxiaOps principal and the
// per-account ExternalId already filled in. Mirrors design §3.1.
function trustPolicyJSON(externalId) {
  return JSON.stringify({
    Version: '2012-10-17',
    Statement: [{
      Sid: 'AllowAxiaOpsToAssumeForReadOnlyScans',
      Effect: 'Allow',
      Principal: { AWS: `arn:aws:iam::${AXIAOPS_AWS_ACCOUNT_ID || '<AxiaOpsAccountId>'}:role/AxiaOpsScanner` },
      Action: 'sts:AssumeRole',
      Condition: { StringEquals: { 'sts:ExternalId': externalId } },
    }],
  }, null, 2);
}

// The least-privilege, read-only permissions policy the customer attaches to the
// same role alongside the trust policy. A role with only the trust policy can be
// assumed but can read nothing, so the scan returns zero resources. This list is
// enumerated from the actual Describe/List calls in
// services/ingestion/internal/provider/aws/ and mirrors docs/production.md
// (AxiaOpsReadOnly) + docs/cross-account-roles-design.md §3.2. No write actions,
// ever. Keep in sync when a new discover_*.go provider call is added.
const SCAN_PERMISSION_ACTIONS = [
  'sts:GetCallerIdentity',
  'ce:GetCostAndUsage',
  'ce:GetCostAndUsageWithResources',
  'cloudwatch:GetMetricStatistics',
  'ec2:DescribeInstances',
  'ec2:DescribeVolumes',
  'ec2:DescribeSnapshots',
  'ec2:DescribeImages',
  'ec2:DescribeAddresses',
  'ec2:DescribeNatGateways',
  'rds:DescribeDBInstances',
  'rds:DescribeDBSnapshots',
  'lambda:ListFunctions',
  'elasticloadbalancing:DescribeLoadBalancers',
  'logs:DescribeLogGroups',
  'ecr:DescribeRepositories',
  'ecr:DescribeImages',
  'secretsmanager:ListSecrets',
  'elasticache:DescribeCacheClusters',
  'es:ListDomainNames',
  'redshift:DescribeClusters',
  'sagemaker:ListEndpoints',
  'dynamodb:ListTables',
  'kinesis:ListStreams',
  'kinesis:DescribeStreamSummary',
  'cloudfront:ListDistributions',
  'eks:ListClusters',
  's3:ListAllMyBuckets',
  's3:GetBucketLocation',
];

function permissionsPolicyJSON() {
  return JSON.stringify({
    Version: '2012-10-17',
    Statement: [{
      Sid: 'AxiaOpsReadOnlyScan',
      Effect: 'Allow',
      Action: SCAN_PERMISSION_ACTIONS,
      Resource: '*',
    }],
  }, null, 2);
}


function BillingSourceConfig({ billingSource, setBillingSource, curConfig, setCurConfig }) {
  const [showAdvanced, setShowAdvanced] = useState(false);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 12, padding: 16, backgroundColor: 'var(--color-surface-alt)', border: '1px solid var(--color-border)', borderRadius: 8 }}>
      <label style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text-mid)' }}>Billing Source</label>
      <div style={{ display: 'flex', gap: 16 }}>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 14 }}>
          <input type="radio" name="billingSource" value="ce" checked={billingSource === 'ce'} onChange={() => setBillingSource('ce')} />
          Cost Explorer (Default)
        </label>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 14 }}>
          <input type="radio" name="billingSource" value="cur_athena" checked={billingSource === 'cur_athena'} onChange={() => setBillingSource('cur_athena')} />
          CUR (Athena)
        </label>
      </div>
      
      {billingSource === 'cur_athena' && (
        <div style={{ marginTop: 8, padding: 12, backgroundColor: 'var(--color-surface)', borderRadius: 6, fontSize: 13, color: 'var(--color-text-muted)' }}>
          <div style={{ marginBottom: 12 }}>
            The CloudFormation stack automatically configures resources with deterministic <strong>axiaops_*</strong> names.
          </div>
          
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, cursor: 'pointer', userSelect: 'none' }}>
            <input type="checkbox" checked={showAdvanced} onChange={e => setShowAdvanced(e.target.checked)} />
            Advanced Configuration (Manual Override)
          </label>

          {showAdvanced && curConfig && setCurConfig && (
            <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 12, paddingLeft: 8, borderLeft: '2px solid var(--color-border)' }}>
              <Field label="Athena Database" value={curConfig.cur_database} onChange={v => setCurConfig({...curConfig, cur_database: v})} placeholder="axiaops_cur_db" mono />
              <Field label="Athena Table" value={curConfig.cur_table} onChange={v => setCurConfig({...curConfig, cur_table: v})} placeholder="axiaops_cur_table" mono />
              <Field label="Athena Workgroup" value={curConfig.cur_workgroup} onChange={v => setCurConfig({...curConfig, cur_workgroup: v})} placeholder="axiaops_athena_wg" mono />
              <Field label="Results S3 Bucket" value={curConfig.cur_results_s3} onChange={v => setCurConfig({...curConfig, cur_results_s3: v})} placeholder="s3://axiaops-athena-results-..." mono />
              <Field label="CUR Region" value={curConfig.cur_region} onChange={v => setCurConfig({...curConfig, cur_region: v})} placeholder="us-east-1" mono />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

async function saveCurConfig(account, billingSource, updateAccountFn, curConfig) {
  if (billingSource === 'cur_athena') {
    return await updateAccountFn(account.id, {
      billing_source: 'cur_athena',
      cur_database: curConfig?.cur_database || 'axiaops_cur_db',
      cur_table: curConfig?.cur_table || 'axiaops_cur_table',
      cur_workgroup: curConfig?.cur_workgroup || 'axiaops_athena_wg',
      cur_results_s3: curConfig?.cur_results_s3 || `s3://axiaops-athena-results-${account.account_id}-${account.region}`,
      cur_region: curConfig?.cur_region || account.region,
    });
  }
  return null;
}


// One-click "Launch Stack" deep link into the customer's CloudFormation console.
// QuickCreate pre-fills the AxiaOpsIntegrationRole template (hosted public on S3
// by aws-infra) and the per-account ExternalId; the customer reviews, ticks the
// IAM-capability acknowledgement, and clicks Create. IAM roles are global, so the
// region in the URL only decides where the (IAM-only, free) stack record lives —
// us-east-1 is always available. Empty AXIAOPS_CFN_TEMPLATE_URL → no button.
function launchStackUrl(externalId) {
  const params = new URLSearchParams({
    templateURL: AXIAOPS_CFN_TEMPLATE_URL,
    stackName: 'AxiaOps-Integration',
    param_ExternalId: externalId,
  });
  // Region goes in the console subdomain, not a query param: CloudFormation's
  // hash-routed SPA never sees the pre-fragment query string, so ?region=… is
  // dropped. The regional host is the reliable way to target a region.
  return `https://us-east-1.console.aws.amazon.com/cloudformation/home#/stacks/quickcreate?${params.toString()}`;
}

// Role tab: two-step flow. Step 1 collects label + region and POSTs /draft.
// Step 2 reveals ExternalId + the trust policy, lets the customer paste back
// their freshly-created role ARN, and runs the verify round-trip.
function RoleAuthTab({ onConnected }) {
  const [step, setStep] = useState('draft'); // 'draft' | 'verify'
  const [draft, setDraft] = useState(null);
  const [label, setLabel] = useState('');
  const [region, setRegion] = useState('eu-central-1');
  const [roleArn, setRoleArn] = useState('');
  const [showManual, setShowManual] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [verifyHint, setVerifyHint] = useState('');
  const [billingSource, setBillingSource] = useState('ce');
  const [curConfig, setCurConfig] = useState({ cur_database: 'axiaops_cur_db', cur_table: 'axiaops_cur_table', cur_workgroup: 'axiaops_athena_wg', cur_results_s3: '', cur_region: '' });
  

  // AXIAOPS_AWS_ACCOUNT_ID must be a real 12-digit AWS account ID. Without
  // a valid value the trust-policy template would render <AxiaOpsAccountId>
  // (unset) or a garbage sentinel literally and the customer would copy a
  // broken policy into AWS. Block the flow with an explicit operator message
  // instead.
  const configMissing = !/^\d{12}$/.test(AXIAOPS_AWS_ACCOUNT_ID);

  async function handleGenerate() {
    setError('');
    setLoading(true);
    try {
      const created = await draftAccount({
        provider: 'aws',
        label: label.trim() || 'My AWS Account',
        region: region.trim() || 'eu-central-1',
        billing_source: billingSource,
      });
      setDraft(created);
      setStep('verify');
    } catch (e) {
      setError('Failed to start onboarding. Please try again.');
    } finally {
      setLoading(false);
    }
  }

  async function handleVerify() {
    if (!roleArn.trim()) {
      setError('Paste your role ARN before verifying.');
      return;
    }
    setError('');
    setVerifyHint('');
    setLoading(true);
    try {
      const result = await verifyAccount(draft.id, { roleArn: roleArn.trim() });
      const updated = await saveCurConfig(result, billingSource, updateAccount, curConfig);
      onConnected(updated || result);
    } catch (e) {
      setError(e.message || 'Verification failed.');
      setVerifyHint(reasonToHint(e.reason));
    } finally {
      setLoading(false);
    }
  }

  if (configMissing) {
    return (
      <ErrorBox
        message="Role-based onboarding is not available in this environment."
        hint="The dashboard was built without VITE_AXIAOPS_AWS_ACCOUNT_ID set. Use Access Keys instead, or contact your administrator."
      />
    );
  }

  if (step === 'draft') {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
        <Field label="Label (optional)" value={label} onChange={setLabel} placeholder="e.g. Production" />
        <Field label="Region" value={region} onChange={setRegion} placeholder="eu-central-1" mono />
        <BillingSourceConfig billingSource={billingSource} setBillingSource={setBillingSource}  />
        {error && <ErrorBox message={error} />}
        <PrimaryButton onClick={handleGenerate} loading={loading} label="Generate connection" />
      </div>
    );
  }

  // step === 'verify'
  const hasLaunchStack = AXIAOPS_CFN_TEMPLATE_URL.startsWith('https://');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <CopyableBlock label="External ID (pre-filled into the stack / your trust policy)" value={draft.external_id} />

      {billingSource === 'cur_athena' ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <a
            href={`/api/v1/accounts/${draft.id}/cur-setup`}
            download="cur_template.yml"
            style={{
              display: 'inline-block', alignSelf: 'flex-start',
              backgroundColor: 'var(--color-accent)', color: '#fff',
              fontSize: 14, fontWeight: 600, padding: '10px 16px',
              borderRadius: 8, textDecoration: 'none',
            }}
          >
            Download CloudFormation Template
          </a>
          <span style={{ fontSize: 12, color: 'var(--color-text-muted)', fontStyle: 'italic' }}>
            Since AxiaOps requires specific regional resources for Cost and Usage Reports, please deploy this template manually.
            Download the file, open the <strong>AWS CloudFormation Console (us-east-1)</strong>, and choose <strong>Upload a template file</strong>.
            The ExternalId and Account ID are already pre-filled inside the file!
          </span>
        </div>
      ) : hasLaunchStack && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <a
            href={launchStackUrl(draft.external_id)}
            target="_blank"
            rel="noopener noreferrer"
            style={{
              display: 'inline-block', alignSelf: 'flex-start',
              backgroundColor: 'var(--color-accent)', color: '#fff',
              fontSize: 14, fontWeight: 600, padding: '10px 16px',
              borderRadius: 8, textDecoration: 'none',
            }}
          >
            Launch Stack in AWS ↗
          </a>
          <span style={{ fontSize: 12, color: 'var(--color-text-muted)', fontStyle: 'italic' }}>
            Opens CloudFormation in a new tab — make sure you're signed in to the AWS
            account you want AxiaOps to scan. The ExternalId is pre-filled; tick the IAM
            acknowledgement and click <strong>Create stack</strong>. When it reaches
            CREATE_COMPLETE, open the stack's <strong>Outputs</strong> tab and copy{' '}
            <code>RoleArn</code> into the field below.
          </span>
        </div>
      )}

      <button
        onClick={() => setShowManual(s => !s)}
        style={{ background: 'none', border: 'none', color: 'var(--color-text-mid)', fontSize: 13, textDecoration: 'underline', cursor: 'pointer', alignSelf: 'flex-start', padding: 0 }}
      >
        {showManual
          ? 'Hide manual setup'
          : (hasLaunchStack ? 'Prefer manual setup (Terraform / console)?' : 'Show policies for manual setup')}
      </button>
      {showManual && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14, paddingLeft: 12, borderLeft: '2px solid var(--color-border)' }}>
          <CopyableBlock label="AxiaOps principal (allowed to assume your role)"
            value={`arn:aws:iam::${AXIAOPS_AWS_ACCOUNT_ID || '<AxiaOpsAccountId>'}:role/AxiaOpsScanner`} />
          <CopyableBlock label="Trust policy JSON" value={trustPolicyJSON(draft.external_id)} />
          <CopyableBlock label="Permissions policy JSON (read-only)" value={permissionsPolicyJSON()} />
          <p style={{ fontSize: 12, color: 'var(--color-text-mid)', margin: 0 }}>
            Create an IAM role named <code>AxiaOpsIntegrationRole</code> with <strong>both</strong> the
            trust policy and the read-only permissions policy, then paste its ARN below. Without the
            permissions policy the role can be assumed but scans return nothing.
          </p>
        </div>
      )}

      <Field label="Role ARN" value={roleArn} onChange={setRoleArn}
        placeholder="arn:aws:iam::...:role/AxiaOpsIntegrationRole"
        hint="From the CloudFormation stack's RoleArn output (or your manually-created role)"
        mono />

      {error && <ErrorBox message={error} hint={verifyHint} />}
      <PrimaryButton onClick={handleVerify} loading={loading} label="Verify and connect" />
    </div>
  );
}

function reasonToHint(reason) {
  switch (reason) {
    case 'trust_policy_mismatch':
      return 'The role does not trust the AxiaOps scanner principal. If you used Launch Stack, re-launch it; if you set the role up manually, expand "manual setup" and re-apply the trust policy.';
    case 'external_id_mismatch':
      return 'The ExternalId on the role does not match this connection. Re-launch the stack (or re-apply the trust policy) with the ExternalId shown above — delete the old stack first.';
    case 'role_not_found':
      return 'AWS could not find that role. Double-check the ARN and that the role exists in the same account.';
    case 'malformed_policy':
      return 'AWS rejected the trust policy as malformed. Compare against the JSON above.';
    case 'access_denied':
      return 'AWS returned AccessDenied. Verify the trust policy and ExternalId condition match exactly.';
    default:
      return '';
  }
}

function AccessKeyTab({ onConnected, isEdit, account, isDark }) {
  const [label, setLabel]             = useState(account?.label ?? '');
  const [accessKeyId, setAccessKeyId] = useState(account?.access_key_id ?? '');
  const [secretKey, setSecretKey]     = useState('');
  const [region, setRegion]           = useState(account?.region ?? 'eu-central-1');
  const [scanIntervalHours, setScanIntervalHours] = useState(account?.scan_interval_hours?.toString() ?? '24');
  const [loading, setLoading]         = useState(false);
  const [error, setError]             = useState('');
  const [billingSource, setBillingSource] = useState(account?.billing_source === 'cur_athena' ? 'cur_athena' : 'ce');
  const [curConfig, setCurConfig] = useState({ 
    cur_database: account?.cur_database || 'axiaops_cur_db', 
    cur_table: account?.cur_table || 'axiaops_cur_table', 
    cur_workgroup: account?.cur_workgroup || 'axiaops_athena_wg', 
    cur_results_s3: account?.cur_results_s3 || '', 
    cur_region: account?.cur_region || '' 
  });
  

  async function handleSubmit() {
    if (!isEdit && (!accessKeyId.trim() || !secretKey.trim())) {
      setError('Access Key ID and Secret Access Key are required.');
      return;
    }
    if (isEdit && !accessKeyId.trim()) {
      setError('Access Key ID is required.');
      return;
    }
    setError('');
    setLoading(true);
    try {
      let result;
      if (isEdit) {
        const scanInterval = parseInt(scanIntervalHours, 10);
        if (isNaN(scanInterval) || scanInterval < 0) {
          setError('Scan interval must be a number ≥ 0.');
          setLoading(false);
          return;
        }
        const updatePayload = {
          label: label.trim() || 'My AWS Account',
          accessKeyId: accessKeyId.trim(),
          secretKey: secretKey.trim() || undefined,
          region: region.trim() || 'eu-central-1',
          scan_interval_hours: scanInterval,
        };
        if (billingSource === 'cur_athena') {
          Object.assign(updatePayload, { 
            billing_source: 'cur_athena',
            cur_database: 'axiaops_cur_db',
            cur_table: 'axiaops_cur_table',
            cur_workgroup: 'axiaops_athena_wg',
            cur_results_s3: `s3://axiaops-athena-results-${account.account_id}-${region.trim() || 'eu-central-1'}`,
            cur_region: region.trim() || 'eu-central-1'
          });
        } else {
          Object.assign(updatePayload, { billing_source: 'ce' });
        }
        result = await updateAccount(account.id, updatePayload);
      } else {
        
        result = await connectAccount({
          provider: 'aws',
          label: label.trim() || 'My AWS Account',
          accessKeyId: accessKeyId.trim(),
          secretKey: secretKey.trim(),
          region: region.trim() || 'eu-central-1',
          billing_source: billingSource,
        });
        const updated = await saveCurConfig(result, billingSource, updateAccount, curConfig);
        result = updated || result;
      }
      onConnected(result);
    } catch {
      setError(isEdit ? 'Failed to update. Check your credentials and try again.' : 'Failed to connect. Check your credentials and try again.');
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      {!isEdit && (
        <div style={{
          backgroundColor: isDark ? 'var(--color-surface-raised)' : '#EFF6FF',
          border: `1px solid ${isDark ? 'var(--color-border)' : '#BFDBFE'}`,
          borderRadius: 10,
          padding: '14px 16px',
          marginBottom: 18,
        }}>
          <span style={{ fontSize: 13, fontWeight: 700, color: isDark ? 'var(--color-text-mid)' : '#1D4ED8', display: 'block', marginBottom: 8 }}>
            Required IAM permissions
          </span>
          <p style={{ fontSize: 12, color: 'var(--color-text-mid)', margin: '0 0 8px' }}>
            Attach this read-only policy to the IAM user behind these access keys.
          </p>
          <CopyableBlock label="Permissions policy JSON" value={permissionsPolicyJSON()} />
        </div>
      )}
      <Field label="Label (optional)" value={label} onChange={setLabel} placeholder="e.g. Production" />
      <Field label="AWS Access Key ID" value={accessKeyId} onChange={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono />
      <Field
        label="AWS Secret Access Key"
        value={secretKey}
        onChange={setSecretKey}
        placeholder={isEdit ? 'Leave blank to keep existing' : 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'}
        mono
        type="password"
      />
      <Field label="Region" value={region} onChange={setRegion} placeholder="eu-central-1" mono />
      {isEdit && (
        <Field
          label="Auto-scan interval (hours)"
          value={scanIntervalHours}
          onChange={setScanIntervalHours}
          placeholder="24"
          type="number"
          hint="0 = on-demand only, or enter hours between automatic scans"
        />
      )}
      <BillingSourceConfig billingSource={billingSource} setBillingSource={setBillingSource}  />
      {error && <ErrorBox message={error} />}
      <PrimaryButton onClick={handleSubmit} loading={loading} label={isEdit ? 'Save Changes' : 'Connect Account'} />
    </>
  );
}

// RoleEditTab covers re-verification of a role-based account that already
// exists. The customer can edit label, region, and scan interval; if the role
// ARN itself needs to change, they paste a new one and we re-verify.
function RoleEditTab({ account, onConnected }) {
  const [label, setLabel] = useState(account.label ?? '');
  const [region, setRegion] = useState(account.region ?? 'eu-central-1');
  const [scanIntervalHours, setScanIntervalHours] = useState(account.scan_interval_hours?.toString() ?? '24');
  const [roleArn, setRoleArn] = useState(account.role_arn ?? '');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [verifyHint, setVerifyHint] = useState('');
  const [billingSource, setBillingSource] = useState(account?.billing_source === 'cur_athena' ? 'cur_athena' : 'ce');
  const [curConfig, setCurConfig] = useState({ cur_database: account?.cur_database || 'axiaops_cur_db', cur_table: account?.cur_table || 'axiaops_cur_table', cur_workgroup: account?.cur_workgroup || 'axiaops_athena_wg', cur_results_s3: account?.cur_results_s3 || '', cur_region: account?.cur_region || '' });

  const roleArnChanged = roleArn.trim() !== (account.role_arn ?? '');

  async function handleSubmit() {
    setError('');
    setVerifyHint('');
    setLoading(true);
    try {
      const scanInterval = parseInt(scanIntervalHours, 10);
      if (isNaN(scanInterval) || scanInterval < 0) {
        setError('Scan interval must be a number ≥ 0.');
        setLoading(false);
        return;
      }
      
      const updatePayload = {
        label: label.trim() || 'My AWS Account',
        region: region.trim() || 'eu-central-1',
        scan_interval_hours: scanInterval,
      };
      
      if (billingSource === 'cur_athena') {
        Object.assign(updatePayload, { 
          billing_source: 'cur_athena',
          cur_database: curConfig.cur_database || 'axiaops_cur_db',
          cur_table: curConfig.cur_table || 'axiaops_cur_table',
          cur_workgroup: curConfig.cur_workgroup || 'axiaops_athena_wg',
          cur_results_s3: curConfig.cur_results_s3 || `s3://axiaops-athena-results-${account.account_id}-${region.trim() || 'eu-central-1'}`,
          cur_region: curConfig.cur_region || region.trim() || 'eu-central-1'
        });
      } else {
        Object.assign(updatePayload, { billing_source: 'ce' });
      }

      // Apply non-credential edits first so label / region / scan interval
      // changes do not get silently dropped when the user also changes the
      // role ARN. PATCH /v1/accounts/{id} ignores role_arn here because it is
      // not in the body — the verify round-trip happens in the second call.
      const updated = await updateAccount(account.id, updatePayload);
      if (roleArnChanged) {
        const verified = await verifyAccount(account.id, { roleArn: roleArn.trim() });
        onConnected(verified);
        return;
      }
      onConnected(updated);
    } catch (e) {
      setError(e.message || 'Failed to update.');
      setVerifyHint(reasonToHint(e.reason));
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      <CopyableBlock label="External ID (read-only)" value={account.external_id ?? ''} />
      <Field label="Label" value={label} onChange={setLabel} placeholder="e.g. Production" />
      <Field label="Region" value={region} onChange={setRegion} placeholder="eu-central-1" mono />
      <Field label="Role ARN" value={roleArn} onChange={setRoleArn} placeholder="arn:aws:iam::...:role/AxiaOpsIntegrationRole" mono
        hint={roleArnChanged ? 'Save will re-verify this role with AWS STS.' : 'Paste a new ARN to re-verify.'} />
      <Field
        label="Auto-scan interval (hours)"
        value={scanIntervalHours}
        onChange={setScanIntervalHours}
        placeholder="24"
        type="number"
        hint="0 = on-demand only, or enter hours between automatic scans"
      />
      <BillingSourceConfig billingSource={billingSource} setBillingSource={setBillingSource} curConfig={curConfig} setCurConfig={setCurConfig} />
      {error && <ErrorBox message={error} hint={verifyHint} />}
      <PrimaryButton onClick={handleSubmit} loading={loading} label="Save Changes" />
    </>
  );
}

function ErrorBox({ message, hint }) {
  return (
    <div style={{ backgroundColor: `var(--color-error)18`, border: `1px solid var(--color-error)40`, borderRadius: 8, padding: '10px 12px' }}>
      <span style={{ fontSize: 13, color: 'var(--color-error)', fontWeight: 500 }}>{message}</span>
      {hint && <div style={{ fontSize: 12, color: 'var(--color-text-mid)', marginTop: 4 }}>{hint}</div>}
    </div>
  );
}

function PrimaryButton({ onClick, loading, label }) {
  return (
    <button
      onClick={onClick}
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
        width: '100%',
        marginTop: 4,
      }}
    >
      {loading
        ? <Spinner size={20} color={'var(--color-text-on-dark)'} />
        : <span style={{ color: 'var(--color-text-on-dark)', fontSize: 15, fontWeight: 700 }}>{label}</span>
      }
    </button>
  );
}

function TabButton({ active, label, sublabel, onClick }) {
  return (
    <button
      onClick={onClick}
      style={{
        flex: 1,
        background: active ? 'var(--color-surface-raised)' : 'transparent',
        border: `1px solid ${active ? 'var(--color-accent)' : 'var(--color-border)'}`,
        borderRadius: 8,
        padding: '10px 12px',
        cursor: 'pointer',
        color: active ? 'var(--color-text)' : 'var(--color-text-mid)',
        fontSize: 13,
        fontWeight: active ? 700 : 500,
        minHeight: 44, // HIG touch-target floor — also helps the two
        // wrapped tabs share row height equally on a narrow viewport.
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 1,
        lineHeight: 1.2,
      }}
    >
      <span>{label}</span>
      {sublabel && (
        <span style={{ fontSize: 10, fontWeight: 500, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.4 }}>
          {sublabel}
        </span>
      )}
    </button>
  );
}

export default function ConnectScreen({ onConnected, onSkip, onCancel, account }) {
  const { isDark } = useTheme();
  const isEdit = !!account;
  const isRoleEdit = isEdit && account.auth_method === 'role';

  // Default to the Role ARN tab on a fresh connect (recommended posture); when
  // editing an existing access-key account, stay on Access Keys to avoid a
  // confusing tab swap. AXIAOPS_AWS_ACCOUNT_ID must be a real 12-digit AWS
  // account ID — without it (unset, empty, or a malformed sentinel like "-")
  // the trust-policy template would render a broken principal, so fall back
  // to Access Keys instead of first-painting an error.
  const hasValidAccountId = /^\d{12}$/.test(AXIAOPS_AWS_ACCOUNT_ID);
  const [activeTab, setActiveTab] = useState(
    isEdit || !hasValidAccountId ? 'access_key' : 'role',
  );

  return (
    <div style={{ minHeight: '100%', backgroundColor: 'var(--color-bg)' }}>
      <div style={{ maxWidth: 560, margin: '0 auto', padding: '32px 20px 64px' }}>

        <div style={{ marginBottom: 28 }}>
          <h1 style={{ fontSize: 22, fontWeight: 800, color: 'var(--color-text)', margin: '0 0 6px' }}>
            {isEdit ? 'Edit AWS Account' : 'Connect AWS Account'}
          </h1>
          <p style={{ fontSize: 14, color: 'var(--color-text-mid)', lineHeight: '21px', margin: 0 }}>
            {isEdit
              ? (isRoleEdit
                  ? 'Update settings or paste a new role ARN to re-verify the connection.'
                  : 'Update credentials or settings. Leave the secret key blank to keep the existing one.')
              : 'Pick how you would like to connect.'}
          </p>
        </div>

        {!isEdit && (
          <div style={{ display: 'flex', gap: 8, marginBottom: 18 }}>
            <TabButton
              active={activeTab === 'role'}
              label="Role ARN"
              sublabel="recommended"
              onClick={() => setActiveTab('role')}
            />
            <TabButton
              active={activeTab === 'access_key'}
              label="Access Keys"
              onClick={() => setActiveTab('access_key')}
            />
          </div>
        )}

        <div style={{
          backgroundColor: 'var(--color-surface)',
          border: `1px solid var(--color-border)`,
          borderRadius: 16,
          padding: '24px',
          display: 'flex',
          flexDirection: 'column',
          gap: 18,
        }}>
          {isRoleEdit ? (
            <RoleEditTab account={account} onConnected={onConnected} />
          ) : activeTab === 'role' ? (
            <RoleAuthTab onConnected={onConnected} />
          ) : (
            <AccessKeyTab onConnected={onConnected} isEdit={isEdit} account={account} isDark={isDark} />
          )}

          {onSkip && (
            <button onClick={onSkip} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '6px', textAlign: 'center', width: '100%' }}>
              <span style={{ fontSize: 14, color: 'var(--color-text-muted)' }}>Skip for now</span>
            </button>
          )}
          {onCancel && (
            <button onClick={onCancel} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '6px', textAlign: 'center', width: '100%' }}>
              <span style={{ fontSize: 14, color: 'var(--color-text-muted)' }}>Cancel</span>
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
