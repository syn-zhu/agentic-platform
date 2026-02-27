FROM ghcr.io/kagent-dev/kagent/kagent-adk:0.7.13

WORKDIR /app

COPY personal_assistant/ personal_assistant/
COPY pyproject.toml .
COPY .python-version .

RUN uv sync --refresh

CMD ["personal_assistant"]
