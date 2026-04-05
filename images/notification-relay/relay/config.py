"""Load fan-out destination config from a YAML file."""
import os
import yaml


def load_destinations(config_path=None):
    """Load destination list from a YAML config file.

    Config format:
        destinations:
          - name: discord
            url: https://discord.com/api/webhooks/...
            headers:
              Content-Type: application/json
            auth_env: DISCORD_WEBHOOK_URL
          - name: pushover
            url: https://api.pushover.net/1/messages.json
            headers: {}
            auth_env: PUSHOVER_TOKEN

    If auth_env is set, the env var's value replaces the url field at load time
    (for destinations where the URL itself is the secret, like Discord webhooks).

    Args:
        config_path: path to YAML config. Defaults to RELAY_CONFIG_PATH env var
                     or /etc/relay/config.yaml.

    Returns:
        List of destination dicts with keys: name, url, headers.
    """
    if config_path is None:
        config_path = os.environ.get(
            "RELAY_CONFIG_PATH", "/etc/relay/config.yaml"
        )

    with open(config_path) as f:
        raw = yaml.safe_load(f)

    destinations = raw.get("destinations", [])
    result = []
    for dest in destinations:
        url = dest.get("url", "")
        auth_env = dest.get("auth_env")
        if auth_env:
            env_val = os.environ.get(auth_env)
            if env_val:
                url = env_val

        result.append({
            "name": dest.get("name", "unnamed"),
            "url": url,
            "headers": dest.get("headers", {}),
        })

    return result
