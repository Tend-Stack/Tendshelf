import AVFoundation
import UIKit

/// What the camera screen can do right now (decision 0048, I4a).
enum ScanAccess: Equatable {
    case ready, ask, denied, noCamera

    static func access(status: AVAuthorizationStatus, hasCamera: Bool) -> ScanAccess {
        guard hasCamera else { return .noCamera }
        switch status {
        case .authorized: return .ready
        case .notDetermined: return .ask
        default: return .denied
        }
    }

    /// The first non-empty scanned text of at most 2048 UTF-16 units. A scanned code is untrusted text: this only picks it,
    /// the shared link parser and the address confirmation judge it.
    static func accept(_ values: [String?]) -> String? {
        for case let value? in values where !value.isEmpty && value.utf16.count <= 2048 {
            return value
        }
        return nil
    }
}

/// The in-app QR reader: the back camera, a hint on top, Cancel at the bottom. It reads a code once, closes itself and
/// hands the text over. It records nothing and sends nothing.
final class ScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    var onResult: (String) -> Void = { _ in }

    private let session = AVCaptureSession()
    private let queue = DispatchQueue(label: "app.tendshelf.ios.scanner")
    private var preview: AVCaptureVideoPreviewLayer?
    private var done = false
    private var token: NSObjectProtocol?

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        configureCamera()
        addControls()
        token = NotificationCenter.default.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { [weak self] _ in
            self?.finish(nil)
        }
    }

    override func viewDidDisappear(_ animated: Bool) {
        super.viewDidDisappear(animated)
        if let token { NotificationCenter.default.removeObserver(token) }
        token = nil
    }

    private func configureCamera() {
        guard let device = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .back),
              let input = try? AVCaptureDeviceInput(device: device), session.canAddInput(input) else { return }
        session.addInput(input)
        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else { return }
        session.addOutput(output)
        output.setMetadataObjectsDelegate(self, queue: .main)
        output.metadataObjectTypes = [.qr]
        let layer = AVCaptureVideoPreviewLayer(session: session)
        layer.videoGravity = .resizeAspectFill
        layer.frame = view.layer.bounds
        view.layer.addSublayer(layer)
        preview = layer
    }

    private func addControls() {
        let hint = UILabel()
        hint.text = words(Words.Key.iosScanHint)
        hint.textColor = .white
        hint.font = .preferredFont(forTextStyle: .headline)
        hint.adjustsFontForContentSizeCategory = true
        hint.numberOfLines = 0
        hint.textAlignment = .center
        hint.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(hint)

        var config = UIButton.Configuration.filled()
        config.title = words(Words.Key.iosScanCancel)
        config.baseBackgroundColor = UIColor(white: 0, alpha: 0.6)
        config.baseForegroundColor = .white
        let cancel = UIButton(configuration: config)
        cancel.accessibilityLabel = words(Words.Key.iosScanCancel)
        cancel.addAction(UIAction { [weak self] _ in self?.finish(nil) }, for: .touchUpInside)
        cancel.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(cancel)

        let guide = view.safeAreaLayoutGuide
        NSLayoutConstraint.activate([
            hint.topAnchor.constraint(equalTo: guide.topAnchor, constant: 24),
            hint.leadingAnchor.constraint(equalTo: guide.leadingAnchor, constant: 24),
            hint.trailingAnchor.constraint(equalTo: guide.trailingAnchor, constant: -24),
            cancel.bottomAnchor.constraint(equalTo: guide.bottomAnchor, constant: -24),
            cancel.centerXAnchor.constraint(equalTo: guide.centerXAnchor),
            cancel.heightAnchor.constraint(greaterThanOrEqualToConstant: 44),
            cancel.widthAnchor.constraint(greaterThanOrEqualToConstant: 120),
        ])
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.layer.bounds
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        let session = session
        queue.async { if !session.isRunning { session.startRunning() } }
    }

    func metadataOutput(_ output: AVCaptureMetadataOutput, didOutput metadataObjects: [AVMetadataObject], from connection: AVCaptureConnection) {
        let values = metadataObjects.map { ($0 as? AVMetadataMachineReadableCodeObject)?.stringValue }
        if let text = ScanAccess.accept(values) { finish(text) }
    }

    /// Stops the camera and closes the screen; the text, if any, goes out once.
    private func finish(_ text: String?) {
        guard !done else { return }
        done = true
        let session = session
        queue.async { if session.isRunning { session.stopRunning() } }
        let result = onResult
        dismiss(animated: true) { if let text { result(text) } }
    }
}
