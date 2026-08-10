-- The identity context's own schema: who has signed in, the sessions they hold, and
-- the sign-ins that are in flight.
--
-- It is a context of its own, with its own migrations and no foreign key to any
-- other context's tables. Nothing here refers to work, and nothing about work refers
-- here by key: an attribution names a principal by its issuer and subject, which are
-- values, so the execution record and this schema stay independently migratable and
-- independently testable.

-- Who has signed in. A principal is identity, never authorization: there is no role
-- and no permission here, because any authenticated principal is the operator.
CREATE TABLE IF NOT EXISTS identity_principals (
    -- Who vouches for the subject: the provider's issuer identifier.
    issuer       text        NOT NULL,
    -- The identifier the issuer uses for this person. Identity is (issuer, subject)
    -- and never the address: an address can be reassigned inside an organisation,
    -- and attribution keyed on one would silently credit somebody else's work to
    -- whoever inherited the mailbox.
    subject      text        NOT NULL,
    -- What to show a human, when the provider disclosed it. Display only.
    display_name text        NOT NULL DEFAULT '',
    email        text        NOT NULL DEFAULT '',
    -- When this principal was first and last seen, so an operator can tell a
    -- long-standing identity from one that appeared today.
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject)
);

-- The signed-in browsers, as the server remembers them. Sessions are records rather
-- than self-describing cookies precisely so ending one is immediate: a stateless
-- credential could only be revoked by invalidating everybody's.
CREATE TABLE IF NOT EXISTS identity_sessions (
    -- The digest of the value the browser holds. The value itself is never stored,
    -- so a copy of this table is not a set of usable credentials.
    token_hash        bytea       PRIMARY KEY,
    issuer            text        NOT NULL,
    subject           text        NOT NULL,
    issued_at         timestamptz NOT NULL,
    -- When the session stops being accepted, whatever the provider's tokens say.
    expires_at        timestamptz NOT NULL,
    -- The provider's tokens, held server-side. They never reach the browser: that is
    -- the whole reason this API is the confidential client rather than the page.
    access_token      text        NOT NULL DEFAULT '',
    refresh_token     text        NOT NULL DEFAULT '',
    -- When the access token stops being usable, or NULL when the provider did not
    -- say, in which case only the session's own expiry bounds it.
    token_expires_at  timestamptz,
    -- A session belongs to a principal in this same context, so the reference is a
    -- key rather than a copied value. Ending a principal ends its sessions with it.
    FOREIGN KEY (issuer, subject)
        REFERENCES identity_principals (issuer, subject) ON DELETE CASCADE
);

-- Sweeping expired sessions is a range scan over this.
CREATE INDEX IF NOT EXISTS identity_sessions_expires_at_idx ON identity_sessions (expires_at);

-- Listing or ending every session of one principal, once such a surface exists.
CREATE INDEX IF NOT EXISTS identity_sessions_principal_idx ON identity_sessions (issuer, subject);

-- The sign-ins that have been started and not completed. The row is what binds a
-- callback to the browser that started it, and taking it is what makes a callback
-- single-use: both properties need a record, because neither can be had from a value
-- the browser carries.
CREATE TABLE IF NOT EXISTS identity_sign_ins (
    -- The digest of the value the browser holds while it visits the provider.
    token_hash    bytea       PRIMARY KEY,
    -- Handed to the provider and expected back unchanged.
    state         text        NOT NULL,
    -- Bound into the provider's identity assertion, so an assertion from another
    -- sign-in does not verify here.
    nonce         text        NOT NULL,
    -- The PKCE verifier whose challenge went to the provider, so an intercepted code
    -- cannot be exchanged by anyone else.
    code_verifier text        NOT NULL,
    -- Where the browser is sent afterwards. Always a path inside this application.
    return_to     text        NOT NULL DEFAULT '/',
    started_at    timestamptz NOT NULL,
    -- When the sign-in stops being completable, so an abandoned one cannot be
    -- finished days later.
    expires_at    timestamptz NOT NULL
);

-- Sweeping abandoned sign-ins is a range scan over this.
CREATE INDEX IF NOT EXISTS identity_sign_ins_expires_at_idx ON identity_sign_ins (expires_at);
