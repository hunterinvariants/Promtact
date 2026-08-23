// Licensed under the Apache License, Version 2.0.

#![forbid(unsafe_code)]

use promtact_rust_tokenring_adapter::adapter::Adapter;
use promtact_rust_tokenring_adapter::codec::{read_frame, write_frame};
use promtact_rust_tokenring_adapter::protocol::{Operation, Request};
use std::error::Error;
use std::io::{self, Write};
use std::process;

fn main() {
    if let Err(error) = run() {
        eprintln!("rust token-ring adapter: {error}");
        process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn Error>> {
    let node_count = parse_node_count()?;
    let mut adapter = Adapter::new(node_count)?;

    let stdin = io::stdin();
    let stdout = io::stdout();
    let mut reader = stdin.lock();
    let mut writer = stdout.lock();

    loop {
        let request: Request = read_frame(&mut reader)?;
        let closing = request.op == Operation::Close;
        let response = adapter.handle(request);

        write_frame(&mut writer, &response)?;
        writer.flush()?;

        if closing {
            return Ok(());
        }
    }
}

fn parse_node_count() -> Result<usize, io::Error> {
    let mut arguments = std::env::args().skip(1);

    let flag = arguments.next();
    let value = arguments.next();
    let extra = arguments.next();

    if flag.as_deref() != Some("--nodes") || value.is_none() || extra.is_some() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "usage: promtact-rust-tokenring-adapter --nodes COUNT",
        ));
    }

    let value = value.expect("node count argument was checked");
    let count = value.parse::<usize>().map_err(|error| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            format!("invalid node count {value:?}: {error}"),
        )
    })?;

    Ok(count)
}
