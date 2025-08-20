# Environment Configuration

## Setup Instructions

1. **Copy the .env template**: Copy the provided `.env` file to your project root directory.

2. **Fill in your values**:
   ```bash
   # OpenAI Configuration
   OPENAI_API_KEY=sk-your-actual-openai-api-key
   OPENAI_MODEL_SMALL=gpt-4o-mini
   OPENAI_MODEL_FINAL=gpt-4o

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
| `OPENAI_API_KEY` | Your OpenAI API key | - | ✅ |
| `OPENAI_MODEL_SMALL` | Model for individual summaries | `gpt-4o-mini` | ❌ |
| `OPENAI_MODEL_FINAL` | Model for final digest | `gpt-4o` | ❌ |
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
