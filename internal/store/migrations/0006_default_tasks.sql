-- youtrack_projects caches the project list so a repository folder can be
-- matched to a project name without a network call. It is replaced as a whole
-- on every refresh; fetched_at is the same for all rows of one refresh.
CREATE TABLE youtrack_projects (
    project_key TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL,
    fetched_at  INTEGER NOT NULL
);

-- default_tasks remembers the issue key of the standard task of each project
-- once it was found or created in YouTrack.
CREATE TABLE default_tasks (
    project TEXT PRIMARY KEY,
    issue   TEXT NOT NULL
);

-- default_task_consent records that the user once confirmed creating the
-- standard task of a project, which lets auto mode create it again later.
CREATE TABLE default_task_consent (
    project      TEXT    PRIMARY KEY,
    confirmed_at INTEGER NOT NULL
);
