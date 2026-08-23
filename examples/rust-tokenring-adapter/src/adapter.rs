// Licensed under the Apache License, Version 2.0.

use crate::protocol::{
    HelloResponse, Message, Operation, RemoteError, Request, Response, Violation, VERSION,
};
use crate::tokenring::{Ring, RingError, TokenMessage};

const TOKEN_MESSAGE_KIND: u8 = 1;
const TOKEN_INVARIANT: &str = "at-most-one-token";

#[derive(Debug)]
pub struct Adapter {
    ring: Ring,
    initialized: bool,
    closed: bool,
}

impl Adapter {
    pub fn new(node_count: usize) -> Result<Self, RingError> {
        Ok(Self {
            ring: Ring::new(node_count)?,
            initialized: false,
            closed: false,
        })
    }

    pub fn handle(&mut self, request: Request) -> Response {
        let mut response = Response {
            version: VERSION,
            id: request.id,
            op: request.op,
            error: None,
            hello: None,
            messages: Vec::new(),
            violation: None,
        };

        if request.version != VERSION {
            set_error(
                &mut response,
                "unsupported-version",
                format!("protocol version {} is not supported", request.version),
            );
            return response;
        }
        if request.id == 0 {
            set_error(
                &mut response,
                "invalid-request",
                "request id must be non-zero",
            );
            return response;
        }
        if !valid_shape(&request) {
            set_error(
                &mut response,
                "invalid-request",
                "request fields do not match the operation",
            );
            return response;
        }
        if self.closed {
            set_error(
                &mut response,
                "adapter-closed",
                "adapter has already closed",
            );
            return response;
        }
        if !self.initialized && request.op != Operation::Hello {
            set_error(
                &mut response,
                "not-initialized",
                "hello must be the first operation",
            );
            return response;
        }

        match request.op {
            Operation::Hello => self.hello(&request, &mut response),
            Operation::Tick => self.tick(&request, &mut response),
            Operation::Drain => self.drain(&request, &mut response),
            Operation::Deliver => self.deliver(&request, &mut response),
            Operation::Check => self.check(&mut response),
            Operation::Close => self.close(),
        }

        response
    }

    pub fn passes(&self) -> u64 {
        self.ring.passes()
    }

    pub fn holder_ids(&self) -> Vec<u32> {
        self.ring.holder_ids()
    }

    fn hello(&mut self, request: &Request, response: &mut Response) {
        if self.initialized {
            set_error(
                response,
                "already-initialized",
                "hello has already completed",
            );
            return;
        }

        let _seed = request
            .hello
            .as_ref()
            .expect("validated hello request")
            .seed;

        self.initialized = true;
        response.hello = Some(HelloResponse {
            nodes: self.ring.node_ids(),
            invariants: vec![TOKEN_INVARIANT.to_owned()],
        });
    }

    fn tick(&mut self, request: &Request, response: &mut Response) {
        let node = request.node.expect("validated tick request");
        if !self.ring.node_ids().contains(&node) {
            set_error(
                response,
                "unknown-node",
                format!("node {node} is not part of the ring"),
            );
            return;
        }

        self.ring.advance(node);
    }

    fn drain(&mut self, request: &Request, response: &mut Response) {
        let node = request.node.expect("validated drain request");
        if !self.ring.node_ids().contains(&node) {
            set_error(
                response,
                "unknown-node",
                format!("node {node} is not part of the ring"),
            );
            return;
        }

        response.messages = self
            .ring
            .take_outbound(node)
            .into_iter()
            .map(encode_message)
            .collect();
    }

    fn deliver(&mut self, request: &Request, response: &mut Response) {
        let node = request.node.expect("validated deliver request");
        if !self.ring.node_ids().contains(&node) {
            set_error(
                response,
                "unknown-node",
                format!("node {node} is not part of the ring"),
            );
            return;
        }

        let message = request.message.as_ref().expect("validated deliver request");

        match decode_message(message) {
            Ok(message) => self.ring.receive(node, message),
            Err(detail) => {
                set_error(response, "invalid-message", detail);
            }
        }
    }

    fn check(&self, response: &mut Response) {
        let holders = self.ring.holder_ids();
        if holders.len() > 1 {
            response.violation = Some(Violation {
                invariant: TOKEN_INVARIANT.to_owned(),
                detail: format!("token held by nodes {holders:?}"),
            });
        }
    }

    fn close(&mut self) {
        self.closed = true;
    }
}

fn valid_shape(request: &Request) -> bool {
    match request.op {
        Operation::Hello => {
            request.hello.is_some() && request.node.is_none() && request.message.is_none()
        }
        Operation::Tick | Operation::Drain => {
            request.hello.is_none() && request.node.is_some() && request.message.is_none()
        }
        Operation::Deliver => {
            request.hello.is_none() && request.node.is_some() && request.message.is_some()
        }
        Operation::Check | Operation::Close => {
            request.hello.is_none() && request.node.is_none() && request.message.is_none()
        }
    }
}

fn encode_message(message: TokenMessage) -> Message {
    Message {
        from: message.from,
        to: message.to,
        kind: TOKEN_MESSAGE_KIND,
        value: digest(&message),
        payload: Some(message.sequence.to_be_bytes().to_vec()),
    }
}

fn decode_message(message: &Message) -> Result<TokenMessage, String> {
    if message.kind != TOKEN_MESSAGE_KIND {
        return Err(format!("message kind {} is not supported", message.kind));
    }

    let payload = message
        .payload
        .as_deref()
        .ok_or_else(|| "token message payload is missing".to_owned())?;

    let sequence_bytes: [u8; 8] = payload.try_into().map_err(|_| {
        format!(
            "token message payload is {} bytes; expected 8",
            payload.len()
        )
    })?;

    let token = TokenMessage {
        from: message.from,
        to: message.to,
        sequence: u64::from_be_bytes(sequence_bytes),
    };

    let expected = digest(&token);
    if message.value != expected {
        return Err(format!(
            "message digest {} does not match expected {}",
            message.value, expected
        ));
    }

    Ok(token)
}

fn digest(message: &TokenMessage) -> u64 {
    (message.sequence << 32) | (u64::from(message.from) << 16) | u64::from(message.to)
}

fn set_error(response: &mut Response, code: impl Into<String>, message: impl Into<String>) {
    response.error = Some(RemoteError {
        code: code.into(),
        message: message.into(),
    });
}
