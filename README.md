# Environment Configuration

## Setup Instructions

1. **Copy the .env template**: Copy the provided `.env` file to your project root directory.

2. **Fill in your values**:
   ```bash
   # Claude (Anthropic) Configuration
   ANTHROPIC_API_KEY=sk-ant-your-actual-anthropic-api-key
   CLAUDE_MODEL_SMALL=claude-haiku-4-5-20251001
   CLAUDE_MODEL_FINAL=claude-sonnet-4-5-20250929

   # Gmail Configuration
   GMAIL_QUERY=label:newsletter is:unread
   TO_EMAIL=your-actual-email@gmail.com

   # Google OAuth Credentials
   GOOGLE_CREDENTIALS_FILE=/path/to/your/credentials.json
   CREDENTIALS_PASSPHRASE=your-secure-passphrase

   # Application Settings
   DRY_RUN=false
   APPEND_SAMPLE=true
   ```

3. **Security**: Add `.env` to your `.gitignore` file to avoid committing sensitive information:
   ```bash
   echo ".env" >> .gitignore
   ```

## How it works

- The application will automatically load the `.env` file when it starts
- Environment variables set in your shell take **precedence** over `.env` file values
- If no `.env` file exists, the application will use default values
- The `.env` file is optional - you can still use traditional environment variables

## All Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `ANTHROPIC_API_KEY` | Your Anthropic API key | - | ✅ |
| `CLAUDE_MODEL_SMALL` | Claude model for individual summaries | `claude-haiku-4-5-20251001` | ❌ |
| `CLAUDE_MODEL_FINAL` | Claude model for final digest | `claude-sonnet-4-5-20250929` | ❌ |
| `GMAIL_QUERY` | Gmail search query | `label:newsletter is:unread` | ❌ |
| `TO_EMAIL` | Email address to send digest to | - | ✅ |
| `GOOGLE_CREDENTIALS_FILE` | Path to Google OAuth credentials | - | ✅ (for setup) |
| `CREDENTIALS_PASSPHRASE` | Passphrase for credential encryption | - | ✅ |
| `DRY_RUN` | Don't mark emails as read | `false` | ❌ |
| `APPEND_SAMPLE` | Include sample bullets in email | `true` | ❌ |

## Example Usage

```bash
# Using .env file (recommended)
./newsletterdigest_go

# Override specific variables
TO_EMAIL="different@email.com" ./newsletterdigest_go

# Dry run mode
DRY_RUN=true ./newsletterdigest_go
```