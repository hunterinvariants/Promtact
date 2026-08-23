// Licensed under the Apache License, Version 2.0.

use serde::de::DeserializeOwned;
use serde::Serialize;
use std::error::Error;
use std::fmt;
use std::io::{self, Read, Write};

pub const MAX_FRAME_SIZE: u32 = 16 << 20;

#[derive(Debug)]
pub enum CodecError {
    Io(io::Error),
    Json(serde_json::Error),
    EmptyFrame,
    FrameTooLarge { size: usize, maximum: usize },
}

impl fmt::Display for CodecError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Io(error) => write!(formatter, "adapter protocol I/O: {error}"),
            Self::Json(error) => {
                write!(formatter, "adapter protocol JSON: {error}")
            }
            Self::EmptyFrame => formatter.write_str("adapter protocol frame is empty"),
            Self::FrameTooLarge { size, maximum } => write!(
                formatter,
                "adapter protocol frame is {size} bytes; maximum is {maximum}"
            ),
        }
    }
}

impl Error for CodecError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Io(error) => Some(error),
            Self::Json(error) => Some(error),
            Self::EmptyFrame | Self::FrameTooLarge { .. } => None,
        }
    }
}

impl From<io::Error> for CodecError {
    fn from(error: io::Error) -> Self {
        Self::Io(error)
    }
}

impl From<serde_json::Error> for CodecError {
    fn from(error: serde_json::Error) -> Self {
        Self::Json(error)
    }
}

pub fn write_frame<W, T>(writer: &mut W, value: &T) -> Result<(), CodecError>
where
    W: Write,
    T: Serialize,
{
    let payload = serde_json::to_vec(value)?;
    let maximum = MAX_FRAME_SIZE as usize;

    if payload.len() > maximum {
        return Err(CodecError::FrameTooLarge {
            size: payload.len(),
            maximum,
        });
    }

    let size = u32::try_from(payload.len()).map_err(|_| CodecError::FrameTooLarge {
        size: payload.len(),
        maximum,
    })?;

    writer.write_all(&size.to_be_bytes())?;
    writer.write_all(&payload)?;
    Ok(())
}

pub fn read_frame<R, T>(reader: &mut R) -> Result<T, CodecError>
where
    R: Read,
    T: DeserializeOwned,
{
    let mut prefix = [0_u8; 4];
    reader.read_exact(&mut prefix)?;

    let size = u32::from_be_bytes(prefix);
    if size == 0 {
        return Err(CodecError::EmptyFrame);
    }
    if size > MAX_FRAME_SIZE {
        return Err(CodecError::FrameTooLarge {
            size: size as usize,
            maximum: MAX_FRAME_SIZE as usize,
        });
    }

    let mut payload = vec![0_u8; size as usize];
    reader.read_exact(&mut payload)?;

    Ok(serde_json::from_slice(&payload)?)
}
