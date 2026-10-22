# OAuth flow storyboard: what happens when you connect

Seven plain-language beats for the motion graphic. Cast: **You**, the **App** (Claude or ChatGPT), the **MCP server**, the **Login service** (our OAuth bridge), **Cognito** (the sign-in page), and the **Music API**.

1. **You add the server.** You paste the MCP server's address into the app.
2. **"Sign in first."** The app knocks; the MCP server answers *401: not signed in* and points to a small info card saying which login service it trusts.
3. **The app introduces itself.** The app reads the login service's card, says who it is (its public ID card, or a quick registration), and secretly keeps a one-time proof it will need later (PKCE).
4. **You sign in.** The app opens your browser at the login service, which sends you on to Cognito's sign-in page. You enter your email and password. Only people in the `mcp-users` group get through.
5. **A one-time code comes back.** Cognito hands the login service a code. The login service checks you're allowed and gives the app its own single-use code, valid for 5 minutes.
6. **Code → key.** The app trades that code, plus its secret proof, for an **access token**, a key stamped "valid only for *this* MCP server" that expires within the hour (it refreshes quietly).
7. **Tools unlock.** Every request now carries the key. The gateway checks the stamp, signature and group before the MCP server sees anything. When a tool needs the music API, the server uses **its own** key; your key never leaves the MCP server.

*Optional end card:* "Your password goes only to the sign-in page. The app only ever holds a short-lived key for one server."
