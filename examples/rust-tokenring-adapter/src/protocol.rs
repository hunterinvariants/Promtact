// Licensed under the Apache License, Version 2.0.

use base64::engine::general_purpose::STANDARD;
use serde::{Deserialize, Serialize};

pub const VERSION: u16 = 1;

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Operation {
    Hello,
    Tick,
    Drain,
    Deliver,
    Check,
    Close,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Request {
    pub version: u16,
    pub id: u64,
    pub op: Operation,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub hello: Option<HelloRequest>,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub node: Option<u32>,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<Message>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Response {
    pub version: u16,
    pub id: u64,
    pub op: Operation,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<RemoteError>,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub hello: Option<HelloResponse>,

    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub messages: Vec<Message>,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub violation: Option<Violation>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct HelloRequest {
    pub seed: i64,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct HelloResponse {
    pub nodes: Vec<u32>,

    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub invariants: Vec<String>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Message {
    pub from: u32,
    pub to: u32,
    pub kind: u8,
    pub value: u64,

    #[serde(
        default,
        skip_serializing_if = "payload_is_empty",
        with = "optional_base64"
    )]
    pub payload: Option<Vec<u8>>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Violation {
    pub invariant: String,
    pub detail: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct RemoteError {
    pub code: String,
    pub message: String,
}

fn payload_is_empty(payload: &Option<Vec<u8>>) -> bool {
    payload.as_ref().is_none_or(Vec::is_empty)
}

mod optional_base64 {
    use super::STANDARD;
    use base64::Engine as _;
    use serde::de::Error as _;
    use serde::{Deserialize, Deserializer, Serializer};

    pub fn serialize<S>(value: &Option<Vec<u8>>, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        match value {
            Some(bytes) => serializer.serialize_some(&STANDARD.encode(bytes)),
            None => serializer.serialize_none(),
        }
    }

    pub fn deserialize<'de, D>(deserializer: D) -> Result<Option<Vec<u8>>, D::Error>
    where
        D: Deserializer<'de>,
    {
        Option::<String>::deserialize(deserializer)?
            .map(|encoded| STANDARD.decode(encoded).map_err(D::Error::custom))
            .transpose()
    }
}
