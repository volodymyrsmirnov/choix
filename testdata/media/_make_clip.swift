// Writes testdata/media/clip.mov: a synthetic 2 s, 64x36, 10 fps H.264
// QuickTime movie whose moov carries the QuickTime metadata keys make,
// model, location.ISO6709 and creationdate. ffmpeg writes these keys where
// AVFoundation does not read them, so the fixture is made with AVAssetWriter.
//
// With the argument "encoders" it instead writes testdata/media/encoders.mov:
// 0.2 s of the same video whose only metadata is QuickTime user data ©too
// ("TOOL-TOO") written before ©enc ("TOOL-ENC").
//
// Run once from the repository root:
//
//	swift testdata/media/_make_clip.swift testdata/media/clip.mov
//	swift testdata/media/_make_clip.swift testdata/media/encoders.mov encoders
import AVFoundation
import CoreVideo
import Foundation

let args = CommandLine.arguments
guard args.count == 2 || (args.count == 3 && args[2] == "encoders") else {
    FileHandle.standardError.write("usage: swift _make_clip.swift <out.mov> [encoders]\n".data(using: .utf8)!)
    exit(2)
}
let encoders = args.count == 3
let out = URL(fileURLWithPath: args[1])
try? FileManager.default.removeItem(at: out)

let width = 64, height = 36, fps: Int32 = 10, frames = encoders ? 2 : 20

let writer = try AVAssetWriter(outputURL: out, fileType: .mov)

func item(_ id: AVMetadataIdentifier, _ value: String) -> AVMetadataItem {
    let m = AVMutableMetadataItem()
    m.identifier = id
    m.value = value as NSString
    m.dataType = kCMMetadataBaseDataType_UTF8 as String
    return m
}
if encoders {
    writer.metadata = [
        item(AVMetadataItem.identifier(forKey: "©too", keySpace: .quickTimeUserData)!, "TOOL-TOO"),
        item(.quickTimeUserDataEncodedBy, "TOOL-ENC"),
    ]
} else {
    writer.metadata = [
        item(.quickTimeMetadataMake, "TESTCAM"),
        item(.quickTimeMetadataModel, "T1"),
        item(.quickTimeMetadataLocationISO6709, "+10.5000+020.2500/"),
        item(.quickTimeMetadataCreationDate, "2026-01-02T03:04:05Z"),
    ]
}

let input = AVAssetWriterInput(mediaType: .video, outputSettings: [
    AVVideoCodecKey: AVVideoCodecType.h264,
    AVVideoWidthKey: width,
    AVVideoHeightKey: height,
    // Every frame a keyframe: with the image generator's default (infinite)
    // tolerances a seek then lands on the nearest frame, so tests can tell
    // frames apart by content.
    AVVideoCompressionPropertiesKey: [AVVideoMaxKeyFrameIntervalKey: 1],
])
input.expectsMediaDataInRealTime = false
let adaptor = AVAssetWriterInputPixelBufferAdaptor(assetWriterInput: input, sourcePixelBufferAttributes: [
    kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA,
    kCVPixelBufferWidthKey as String: width,
    kCVPixelBufferHeightKey as String: height,
])
writer.add(input)
guard writer.startWriting() else {
    fatalError("startWriting: \(String(describing: writer.error))")
}
writer.startSession(atSourceTime: .zero)

for i in 0..<frames {
    while !input.isReadyForMoreMediaData { usleep(1000) }
    guard let pool = adaptor.pixelBufferPool else { fatalError("no pixel buffer pool") }
    var pb: CVPixelBuffer?
    CVPixelBufferPoolCreatePixelBuffer(nil, pool, &pb)
    guard let buf = pb else { fatalError("no pixel buffer") }
    CVPixelBufferLockBaseAddress(buf, [])
    let base = CVPixelBufferGetBaseAddress(buf)!.assumingMemoryBound(to: UInt8.self)
    let stride = CVPixelBufferGetBytesPerRow(buf)
    // A gradient whose red channel grows with the frame index, so the order
    // of extracted frames is observable.
    for y in 0..<height {
        for x in 0..<width {
            let p = base + y * stride + x * 4
            p[0] = UInt8((x * 4 + i * 12) & 0xff) // B
            p[1] = UInt8((y * 7) & 0xff)          // G
            p[2] = UInt8((i * 12) & 0xff)         // R
            p[3] = 0xff
        }
    }
    CVPixelBufferUnlockBaseAddress(buf, [])
    guard adaptor.append(buf, withPresentationTime: CMTime(value: CMTimeValue(i), timescale: fps)) else {
        fatalError("append: \(String(describing: writer.error))")
    }
}
input.markAsFinished()
writer.endSession(atSourceTime: CMTime(value: CMTimeValue(frames), timescale: fps))
let done = DispatchSemaphore(value: 0)
writer.finishWriting { done.signal() }
done.wait()
guard writer.status == .completed else {
    fatalError("finishWriting: \(String(describing: writer.error))")
}

if encoders {
    // AVAssetWriter writes ©enc before ©too whatever the order given, and
    // AVFoundation reports items in file order. Swap the two equal-sized
    // udta atoms so ©too comes first: a reader that takes the first encoder
    // item it sees then picks the wrong one.
    var data = try Data(contentsOf: out)
    func atom(_ name: [UInt8]) -> Range<Int> {
        guard let r = data.range(of: Data(name)) else { fatalError("atom \(name) not found") }
        let start = r.lowerBound - 4
        let size = Int(data[start]) << 24 | Int(data[start + 1]) << 16 | Int(data[start + 2]) << 8 | Int(data[start + 3])
        return start..<(start + size)
    }
    let enc = atom([0xA9, 0x65, 0x6E, 0x63]), too = atom([0xA9, 0x74, 0x6F, 0x6F])
    guard enc.count == too.count, enc.upperBound == too.lowerBound else {
        fatalError("unexpected udta layout: ©enc \(enc), ©too \(too)")
    }
    let encBytes = data.subdata(in: enc), tooBytes = data.subdata(in: too)
    data.replaceSubrange(enc.lowerBound..<too.upperBound, with: tooBytes + encBytes)
    try data.write(to: out)
}
