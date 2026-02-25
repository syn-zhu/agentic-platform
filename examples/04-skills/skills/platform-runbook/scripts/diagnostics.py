#!/usr/bin/env python3
"""Local agent diagnostics — reports environment and configuration."""

import os
from pathlib import Path

SA_PATH = "/var/run/secrets/kubernetes.io/serviceaccount"
SKILLS_DIR = "/skills"


def main():
    print("=" * 55)
    print("  Agent Environment Diagnostics")
    print("=" * 55)
    print()

    # Namespace and identity
    ns_file = Path(SA_PATH) / "namespace"
    namespace = ns_file.read_text().strip() if ns_file.exists() else "unknown"
    agent_name = os.environ.get("KAGENT_NAME", "unknown")
    agent_url = os.environ.get("KAGENT_URL", "unknown")

    print(f"  Agent Name:     {agent_name}")
    print(f"  Namespace:      {namespace}")
    print(f"  Controller URL: {agent_url}")
    print(f"  Skills Folder:  {os.environ.get('KAGENT_SKILLS_FOLDER', 'not set')}")
    print()

    # Service account
    token_exists = Path(SA_PATH, "token").exists()
    ca_exists = Path(SA_PATH, "ca.crt").exists()
    print(f"  SA Token:       {'present' if token_exists else 'MISSING'}")
    print(f"  CA Cert:        {'present' if ca_exists else 'MISSING'}")
    print()

    # Loaded skills
    skills_path = Path(SKILLS_DIR)
    if skills_path.exists():
        skills = []
        for d in sorted(skills_path.iterdir()):
            if d.is_dir() and (d / "SKILL.md").exists():
                skills.append(d.name)
        print(f"  Loaded Skills:  {len(skills)}")
        for s in skills:
            scripts_dir = skills_path / s / "scripts"
            script_count = len(list(scripts_dir.iterdir())) if scripts_dir.exists() else 0
            print(f"    - {s} ({script_count} script(s))")
    else:
        print("  Loaded Skills:  none (skills directory not found)")
    print()

    # LLM configuration
    print("  LLM Config:")
    for key in ["ANTHROPIC_API_KEY", "OPENAI_API_KEY"]:
        val = os.environ.get(key)
        if val:
            masked = val[:8] + "..." if len(val) > 8 else "***"
            print(f"    {key}: {masked}")
    print()

    # Tracing
    otel_endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "not configured")
    tracing = os.environ.get("OTEL_TRACING_ENABLED", "false")
    print(f"  Tracing:        {tracing}")
    print(f"  OTLP Endpoint:  {otel_endpoint}")
    print()

    print("=" * 55)
    print("  Diagnostics complete")
    print("=" * 55)


if __name__ == "__main__":
    main()
