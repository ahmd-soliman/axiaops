# Source Control — Free Tools

## GitLab (Recommended — Already in Use)

**Free tier includes:**
- Unlimited private repositories
- 5 GB storage per repo
- 400 CI/CD minutes/month (shared runners)
- GitLab Issues, Boards, and Milestones
- Merge request approvals
- Container Registry (free)
- Package Registry (free)
- Wiki per project
- GitLab Pages (static site hosting)

**Why it's the right choice for AxiaOps:**
- Already using it (`gitlab.com/axiaops-works/axiaopsops`)
- CI/CD, issue tracking, container registry all in one place — no tool sprawl
- GitLab CI is more powerful than GitHub Actions for self-hosted runners
- Free private groups and subgroups

**Limitations of free tier:**
- 400 CI/CD minutes/month (upgrade or use self-hosted runner to remove limit)
- No advanced security scanning (SAST/DAST free but limited)
- No code owners enforcement on free

---

## Self-Hosted Runner (Removes CI Minute Limit)

Register a GitLab Runner on your own machine or a cheap VPS to bypass the 400 minute/month limit entirely.

```bash
# Install on any Linux VPS or local Mac
brew install gitlab-runner          # macOS
gitlab-runner register              # connect to your GitLab project
```

**Cost:** Free. A €5/month Hetzner VPS is enough for a solo project.

---

## Git Clients (Desktop)

| Tool | Platform | Free |
|------|----------|------|
| **GitKraken** | Mac/Win/Linux | Free for public repos; limited on private |
| **Sourcetree** | Mac/Win | Fully free |
| **Fork** | Mac/Win | Free (nagware, no hard limit) |
| **VS Code / Cursor built-in** | Any | Fully free |

**Recommendation:** Use the terminal or VS Code built-in — no extra tool needed.
