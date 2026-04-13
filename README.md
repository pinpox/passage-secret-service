# passage-secret-service

A lightweight [freedesktop Secret
Service](https://specifications.freedesktop.org/secret-service/latest-single/)
provider that stores secrets using
[passage](https://github.com/FiloSottile/passage) (age encryption).

This is an alternative to gnome-keyring or kwallet for systems that already use
passage for password management. Applications like Git Butler, Electron apps,
and anything using libsecret will work with it.

It registers as standard `org.freedesktop.secrets` on the D-Bus session bus
(like e.g. gnome-keyring does) and uses the `passage` CLI to store secrets
age-encrypted. Addditional item metadata (labels, attributes, timestamps) is
stored as JSON files in `$XDG_DATA_HOME/passage-secret-service/`.

## Requirements

- `passage` must be installed and configured (recipients + identity files)
- A running D-Bus session bus

## Usage

Run the service. That's it. It registers on D-Bus like any other freedesktop
copliant secrets service.

`passage-secret-service` runs in the foreground. For proper deployment you might
want to use a systemd user service.

## Testing

`secret-tool` ( provided by `libsecret`) can be used for testing:

```
# Store a secret
echo -n "my-password" | secret-tool store --label="Test" service myapp user me

# Retrieve it
secret-tool lookup service myapp

# Search
secret-tool search service myapp
```

Secrets live in your passage store under `secret-service/`:

```
~/.passage/store/secret-service/
└── default/
    └── myapp/
        └── a1b2c3d4      # age-encrypted secret
```
