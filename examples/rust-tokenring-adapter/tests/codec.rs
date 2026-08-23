// Licensed under the Apache License, Version 2.0.

use promtact_rust_tokenring_adapter::codec::{read_frame, write_frame, CodecError, MAX_FRAME_SIZE};
use promtact_rust_tokenring_adapter::protocol::{HelloRequest, Operation, Request, VERSION};
use std::io::{self, Cursor, Write};

fn hello_request() -> Request {
    Request {
        version: VERSION,
        id: 1,
        op: Operation::Hello,
        hello: Some(HelloRequest { seed: -17 }),
        node: None,
        message: None,
    }
}

fn frame(payload: &[u8]) -> Vec<u8> {
    let size = u32::try_from(payload.len()).expect("test payload fits u32");
    let mut result = size.to_be_bytes().to_vec();
    result.extend_from_slice(payload);
    result
}

#[test]
fn frame_matches_go_prefix_and_round_trips() {
    let request = hello_request();
    let expected_json = br#"{"version":1,"id":1,"op":"hello","hello":{"seed":-17}}"#;

    let mut encoded = Vec::new();
    write_frame(&mut encoded, &request).expect("write frame");

    assert_eq!(
        &encoded[..4],
        &u32::try_from(expected_json.len())
            .expect("golden JSON fits u32")
            .to_be_bytes()
    );
    assert_eq!(&encoded[4..], expected_json);

    let decoded: Request = read_frame(&mut Cursor::new(encoded)).expect("read frame");
    assert_eq!(decoded, request);
}

#[test]
fn read_rejects_invalid_frame_boundaries() {
    let error = read_frame::<_, Request>(&mut Cursor::new(Vec::<u8>::new()))
        .expect_err("missing prefix must fail");
    assert!(matches!(
        error,
        CodecError::Io(ref error)
            if error.kind() == io::ErrorKind::UnexpectedEof
    ));

    let error = read_frame::<_, Request>(&mut Cursor::new([0_u8; 4]))
        .expect_err("zero-sized frame must fail");
    assert!(matches!(error, CodecError::EmptyFrame));

    let oversized = (MAX_FRAME_SIZE + 1).to_be_bytes();
    let error = read_frame::<_, Request>(&mut Cursor::new(oversized))
        .expect_err("oversized frame must fail");
    assert!(matches!(
        error,
        CodecError::FrameTooLarge {
            size,
            maximum
        } if size == MAX_FRAME_SIZE as usize + 1
            && maximum == MAX_FRAME_SIZE as usize
    ));

    let error = read_frame::<_, Request>(&mut Cursor::new(frame(b"{}")))
        .expect_err("incomplete request JSON must fail");
    assert!(matches!(error, CodecError::Json(_)));

    let truncated = [0_u8, 0, 0, 5, b'{', b'}'];
    let error = read_frame::<_, Request>(&mut Cursor::new(truncated))
        .expect_err("truncated payload must fail");
    assert!(matches!(
        error,
        CodecError::Io(ref error)
            if error.kind() == io::ErrorKind::UnexpectedEof
    ));
}

#[test]
fn write_rejects_oversized_json() {
    let oversized = "x".repeat(MAX_FRAME_SIZE as usize + 1);

    let error = write_frame(&mut Vec::new(), &oversized).expect_err("oversized JSON must fail");

    assert!(matches!(
        error,
        CodecError::FrameTooLarge {
            size,
            maximum
        } if size > MAX_FRAME_SIZE as usize
            && maximum == MAX_FRAME_SIZE as usize
    ));
}

#[derive(Default)]
struct ShortWriter {
    bytes: Vec<u8>,
    maximum_write: usize,
}

impl Write for ShortWriter {
    fn write(&mut self, input: &[u8]) -> io::Result<usize> {
        let count = input.len().min(self.maximum_write);
        self.bytes.extend_from_slice(&input[..count]);
        Ok(count)
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[test]
fn write_completes_partial_writes() {
    let request = hello_request();
    let mut writer = ShortWriter {
        bytes: Vec::new(),
        maximum_write: 3,
    };

    write_frame(&mut writer, &request).expect("write through short writer");

    let decoded: Request = read_frame(&mut Cursor::new(writer.bytes)).expect("read frame");
    assert_eq!(decoded, request);
}

struct ZeroWriter;

impl Write for ZeroWriter {
    fn write(&mut self, _input: &[u8]) -> io::Result<usize> {
        Ok(0)
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[test]
fn write_rejects_zero_progress() {
    let error =
        write_frame(&mut ZeroWriter, &hello_request()).expect_err("zero-progress writer must fail");

    assert!(matches!(
        error,
        CodecError::Io(ref error)
            if error.kind() == io::ErrorKind::WriteZero
    ));
}
