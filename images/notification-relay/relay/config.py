"""Load fan-out destination config from a YAML file."""
import os
import yaml
from relay.destination import Destination


def load_destinations(config_path=None):
    """Load destination list from a YAML config file.

    Config format:
        destinations:
          - name: discord
            url: https://placeholder.example.com
            headers:
              Content-Type: application/json
            url_env: DISCORD_WEBHOOK_URL
          - name: pushover
            url: https://api.pushover.net/1/messages.json
            headers: {}

    If url_env is set, the env var's value replaces the url field at load time.
    This is for destinations where the URL itself is the secret (e.g. Discord
    webhooks contain an auth token in the URL path).

    Args:
        config_path: path to YAML config. Defaults to RELAY_CONFIG_PATH env var
                     or /etc/relay/config.yaml.

    Returns:
        List of Destination objects.
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
        url_env = dest.get("url_env")
        if url_env:
            env_val = os.environ.get(url_env)
            if env_val:
                url = env_val

        result.append(Destination(
            name=dest.get("name", "unnamed"),
            url=url,
            headers=dest.get("headers", {}),
        ))

    return result
