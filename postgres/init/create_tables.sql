CREATE TABLE Exec (
    command VARCHAR(4096),
    run_at TIMESTAMP
);

CREATE TABLE Changes (
    path VARCHAR(4096),
    event VARCHAR(64),
    occured_at TIMESTAMP
);