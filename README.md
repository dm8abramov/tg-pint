# tg-pint

Telegram bot for group chats. It keeps a short in-memory context per chat and answers when users:

- write any message in a private chat
- mention the bot in a group, for example `@your_bot что думаешь?`
- reply to the bot's message in a group
- send `/ask your question`
- send `/set_promt new system prompt` to change the system prompt until restart (`/set_prompt` also works)
- send `/set_probability 0.5` to change random group reply probability until restart
- send `/settings` to show the current runtime settings
- write regular group messages, with the configured probability

The bot uses an OpenAI-compatible LLM API. For a free hosted option, create a free OpenRouter key and choose any currently available `:free` model in the OpenRouter model list.

## Run

Create a bot with BotFather, add it to a group, then create a local config:

```bash
make env
```

Edit `.env`:

```dotenv
TELEGRAM_BOT_TOKEN=123:telegram-token
LLM_BASE_URL=https://openrouter.ai/api/v1
LLM_API_KEY=openrouter-key
LLM_MODEL=provider/model:free
REPLY_PROBABILITY=0.5
LLM_DEBUG_LOG=false
SYSTEM_PROMPT="Ты полезный участник группового чата. Отвечай коротко."
```

`REPLY_PROBABILITY=0.5` means the bot replies to about half of regular group messages. Private messages, direct commands, mentions, and replies to the bot are always answered.
Set `LLM_DEBUG_LOG=true` to log full LLM request and response JSON.

Start the bot:

```bash
make run
```

Or run it in Docker. The image does not include `.env`; runtime variables are passed from the local file:

```bash
make docker-run
```

Other commands:

```bash
make test
make build
make fmt
make docker-build
```

For full group-chat context, disable BotFather privacy mode so Telegram forwards regular group messages to the bot. With privacy mode enabled, use commands and replies to the bot.
