FROM ghcr.io/kagent-dev/kagent/kagent-adk:0.7.13

WORKDIR /app

COPY mcp_deployer/ mcp_deployer/
COPY pyproject.toml .
COPY .python-version .

RUN uv sync --refresh

CMD ["mcp_deployer"]
