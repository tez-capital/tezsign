### TezSign as a Service with AMI

`ami` is a utility for portable application management, providing a unified interface and portability for managed apps.

#### Device Setup

For detailed instructions on device setup, please refer to the main README under the [Setup](https://github.com/tez-capital/tezsign/blob/main/readme.md#%EF%B8%8F-setup) section.

#### Setup

> **NOTE:** You do not need to specify the `path` when running `ami` commands if your current working directory (cwd) matches the path you want to run the command against. In this tutorial, we explicitly use the path for clarity.

1.  **Install AMI**
    `wget -q https://raw.githubusercontent.com/alis-is/ami/master/install.sh -O /tmp/install.sh && sudo sh /tmp/install.sh`

2.  **Create a directory**
    Create a directory outside of the `/home` directory tree. For example:
    `mkdir /ami-apps/tezsign`

3.  **Create the configuration file**
    Create `/ami-apps/tezsign/app.json` with the following content:

    ```yaml
    {
        configuration: {
            BACKEND: tezsign
            // optionally define SIGNER_ENDPOINT
            // SIGNER_ENDPOINT: 127.0.0.1:20090
        },
        id: tezsign,
        type: {
                id: xtz.signer
                version: latest
        },
        // NOTE: actual tezsign services use <username>_tezsign as its user
        // this is to isolate tezsign into a separate context
        user: <username>
    }
    ```

4.  **Set up the app**
    `ami --path=/ami-apps/tezsign setup`

5.  **Start the service**
    Start the TezSign service:
    `ami --path=/ami-apps/tezsign start`

6.  **Check status**
    Check the TezSign status:
    `ami --path=/ami-apps/tezsign info`

You can now run `tezsign`-specific commands using:
`ami --path=/ami-apps/tezsign tezsign`

---

#### Configuration Change

If you need to modify the configuration:

1.  **Stop the app:**
    `ami --path=<your app path> stop`
2.  **Edit configuration:**
    Change `app.json` or `app.hjson` as required.
3.  **Reconfigure:**
    `ami --path=<your app path> setup --configure`
4.  **Restart the app:**
    `ami --path=<your app path> start`

---

#### Removing the App

To completely remove the application:

1.  **Stop the app:**
    `ami --path=<your app path> stop`
2.  **Remove the app:**
    `ami --path=<your app path> remove --all`

---

#### Troubleshooting

To enable trace-level logging, run `ami` with the `-ll=trace` flag.

For example:
`ami --path=<your app path> -ll=trace setup`

> **Reminder:** Always adjust `<your app path>` according to your app's actual location.